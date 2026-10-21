# Edge and HTTP Ingress Security

Sub2API supports long-lived SSE and WebSocket requests. Protect the request
ingress without imposing a response `WriteTimeout`: a write deadline would
terminate healthy long generations and streams.

## Application defaults

- `server.max_header_bytes: 65536` limits HTTP/1 request headers to 64 KiB;
  Go maps it to the corresponding HTTP/2 header-list limit.
- `server.read_header_timeout: 10` bounds slow-header attacks. It does not
  limit request processing or response streaming.
- `server.max_request_body_size: 268435456` is the absolute 256 MiB safety net.
- `gateway.max_body_size: 268435456` remains available to multimodal, Gemini,
  image, video, and batch-image endpoints.
- `gateway.text_max_body_size: 33554432` limits the known pure-text
  `/embeddings` and `/alpha/search` endpoints to 32 MiB.
- H2C defaults to 50 concurrent streams per connection, a 2 MiB connection
  upload window, and a 512 KiB stream upload window.
- Invalid credential abuse is limited in process by trusted client IP (IPv6
  `/64`): 120 failures per 60 seconds followed by a 60-second block. This is a
  per-instance safety net; multi-instance enforcement still belongs at the
  load balancer, CDN, or WAF.

Do not add a single application-wide request semaphore: an SSE request may
legitimately occupy it for many minutes. Apply connection and unauthenticated
request controls at the edge; authenticated user/API-key concurrency remains
the application's responsibility.

## Chat 对话的部署约束

Chat 的模型流量直接进入现有 `/v1/chat/completions`，历史和附件使用需要登录的
`/api/v1/chat/`。沿用网关现有的 API 密钥限制、计费与真实客户端 IP 策略。
原文件每个最多 20 MiB、每条消息最多 5 个，原文件与提取文本合计每个用户最多 100,000,000 字节。
PDF 在浏览器提取文字，最多 200 页、2,000,000 字节 UTF-8 文本，不提供 OCR。
图片以内联 Base64 发送；完整模型请求（含历史与 JSON）最多 95,000,000 字节，
如 `server.max_request_body_size` 或 `gateway.max_body_size` 更小，则使用更小值。
超过限制会明确报错，不自动截断历史。代理限制更小时也应相应调低应用配置。

历史表由启动迁移 `custom_eezd_20261021_001_chat.sql` 创建，正常升级无需手动重复执行。
原文件为私有文件，目录由 `chat.attachment_dir` / `CHAT_ATTACHMENT_DIR` 指定，
默认 `${DATA_DIR}/chat`（未设置 DATA_DIR 时为 `./data/chat`）。标准 Docker 工作目录
下为 `/app/data/chat`，复用已有 data 持久卷。备份/恢复必须将数据库与此目录作为
同一组数据处理；多实例必须共享此文件系统。不要将此目录暴露为 Nginx 静态目录。
页面离开或停止会取消请求；异常关闭页面时可能仅保留最近 2 秒内已保存的内容，
30 秒无心跳的生成会标记中断。未发送附件仍占配额，可从该会话移除或随会话删除。

对于经过 Cloudflare 的站点，HTTP 请求体应限制为 **100,000,000 字节**，
不能把 `100m`（104,857,600 字节）当成十进制 100 MB。此限制包含 multipart、
JSON、Base64 和历史，并不等于原文件大小。Cloudflare 的区域设置也可能进一步
降低限制，源站 Nginx 无法提高 CDN 上限。

本项目现有两处站点的配置调整位置如下；这些是部署准备，修改线上文件和重载
Nginx 需要单独授权：

- `/etc/nginx/conf.d/588996.xyz/ai.588996.xyz.conf`
- `/etc/nginx/conf.d/134333.xyz/ai-relay.134333.xyz.conf`

在各自现有 `server` 中设置（保留 TLS、真实 IP、鉴权等配置）：

```nginx
client_max_body_size 100000000;
```

在各自代理到 Sub2API 的现有 `location /` 中设置，保留原有 `proxy_pass` 和转发头：

```nginx
proxy_http_version 1.1;
proxy_buffering off;
proxy_request_buffering off;
proxy_cache off;
proxy_read_timeout 1800s;
proxy_send_timeout 1800s;
```

不要在 `gzip_types` 中加入 `text/event-stream`；如果有专门的 SSE location，
可在其中设置 `gzip off;`。Cloudflare 缓存规则也应绕过 `/api/v1/chat/*` 与
`/v1/chat/completions`。附件响应已有 `Cache-Control: private, no-store`，
仍应确认边缘规则没有强制缓存。正式部署时先 `nginx -t` 再重载，并经过真实域名
验证上传、413、停止请求、长时间 SSE 和附件权限；本地测试不能替代 CDN 实链验收。

## Trusted client IPs

`security.trust_forwarded_ip_for_api_key_acl` is enabled by default for upgrade
compatibility. While enabled, raw forwarding headers take over client-IP
resolution for logs and security-sensitive paths. Custom headers from
`security.forwarded_client_ip_headers` are checked in configured order before
the built-in `CF-Connecting-IP`, `X-Real-IP`, and `X-Forwarded-For` fallback.
Header names are case-insensitive, normalized when loaded, de-duplicated, and
limited to 16 unique valid HTTP field names. Header values must contain IP
literals; comma-separated values are supported, invalid entries are skipped,
and public addresses are preferred over private fallback addresses.

The list can be supplied in YAML or with the comma-separated environment
variable `SECURITY_FORWARDED_CLIENT_IP_HEADERS`; an explicitly empty environment
value clears YAML values. It is also editable from the admin security settings
and updates at runtime without a restart. A request snapshots the switch and
header list together, so one request cannot mix old and new settings. Custom
headers are ignored completely when the switch is disabled. In that mode Gin's
`server.trusted_proxies` chain is authoritative: configure only the exact
CIDR/IP addresses that connect directly to Sub2API. An explicit empty list
trusts no forwarded client IPs.

When `server.trusted_proxies` is omitted, only `127.0.0.1/32` and `::1/128`
are trusted, so local Nginx/1Panel proxies can pass the visitor address without
extra configuration. This trusts forwarding headers supplied by local
processes; set an explicit empty list to disable that trust. Explicit lists
replace the defaults. Docker bridge addresses, private subnets and CDN peers
are never added automatically. Changes require restarting Sub2API. These
defaults also apply to auth IP guards and IP risk control, which always use
the trusted proxy chain regardless of the compatibility switch.

On the first upgrade to this mode, a legacy `false` value is changed to `true`
only when `server.trusted_proxies` was not explicitly configured; explicit
proxy policies remain in secure mode. New installations persist the configured
custom header list during database initialization. Existing installations
backfill a missing database value from the YAML configuration. A hidden
migration marker prevents later administrator changes from being overwritten.
If settings cannot be read or the persisted custom-header list is malformed,
the process fails closed to trusted-proxy mode with no custom headers. If a
migration write fails, the computed mode remains active for the current process
and startup records a warning.

Compatibility takeover accepts forwarded headers without validating the direct
peer, including any configured custom header. Protect the origin from direct
access while it is enabled. A CDN deployment must firewall the origin so only
the CDN or load balancer can reach it, and that proxy must overwrite every
trusted client-IP header rather than append an untrusted client value.

Example for a proxy on the same host:

```yaml
server:
  trusted_proxies:
    - 127.0.0.1/32
    - ::1/128
```

## Nginx baseline

Define shared zones in the `http` block. Tune rates to measured legitimate
traffic; the values below are conservative starting points, not universal
capacity targets.

```nginx
limit_conn_zone $binary_remote_addr zone=sub2api_conn:20m;
limit_req_zone  $binary_remote_addr zone=sub2api_auth:20m rate=5r/s;
limit_req_zone  $binary_remote_addr zone=sub2api_api:40m rate=30r/s;
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl http2;
    server_name api.example.com;

    client_header_timeout 10s;
    client_max_body_size 256m;
    large_client_header_buffers 4 16k;
    limit_conn sub2api_conn 40;

    location ~ ^/(auth|api/auth)/ {
        limit_req zone=sub2api_auth burst=10 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location ~ ^/(v1/)?(embeddings|alpha/search)$ {
        client_max_body_size 32m;
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_pass http://127.0.0.1:8080;
    }

    location / {
        limit_req zone=sub2api_api burst=60 nodelay;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1800s;
        proxy_send_timeout 1800s;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

If Nginx gzip is enabled in the `http` block, keep `text/event-stream` out of
`gzip_types` and do not use `gzip_types *` for Sub2API. The
`proxy_buffering off` setting above prevents proxy buffering, but it does not
disable the gzip response filter. Use an explicit list for ordinary responses:

```nginx
gzip on;
gzip_types text/plain text/css application/json application/javascript application/xml image/svg+xml;
```

If a shared global configuration cannot exclude SSE by content type, set
`gzip off;` in the locations serving streaming API routes. This leaves gzip
available for the web UI and static assets.

Do not use an incoming `$http_x_forwarded_for` value unless Nginx real-IP
processing is restricted to explicit trusted proxy CIDRs.

## 生图工具台的部署约束

`/image-workbench` 使用 JWT 私有的 `/api/v1/image-workbench/*`，应在 CDN 与
反向代理中绕过缓存；服务端响应包含 `Cache-Control: private, no-store`。
上传每张最多 20 MiB、最多 5 张参考图，每次生成 1–4 张，参考图与生成图片独立
占用每用户 100,000,000 字节，和 Chat 配额分开。提交时每张结果先预留 20 MiB，
完成后只保留实际用量。空间不足需清理未使用上传或生成历史。

任务提交立即返回，页面通过短请求轮询；离页不会取消后台生成，不需要让浏览器
维持长达 30 分钟的 HTTP 连接。取消可能无法撤销已到达上游的请求或费用。
Images 与 Gemini 请求仍经过完整现有网关；单张分别调用，上游失败不会由工具台
自动重试。Gemini 使用流式接口以遵守分组的“仅流式”约束。
完整参考图编码请求最多 100,000,000 字节，应用已有入口限制更小时取更小值。
已有 Nginx 请求体限制与真实 IP 配置沿用前述规则。

迁移 `custom_eezd_20261021_002_image_workbench.sql` 由正常启动执行，无需手动重复
执行。图片放在 `${DATA_DIR}/image-workbench`（默认 `./data/image-workbench`，标准
Docker 为 `/app/data/image-workbench`），复用 data 持久卷，不能映射成静态目录。
备份/恢复需要同时包含数据库和此目录。当前任务运行器面向单应用实例；不要在
共享数据库上并行启动多个应用实例，启动恢复会将旧运行任务标为中断并释放预留。
重启不自动重放模型请求；已保存结果保留，中断请求仍可能已被上游计费。

## Caddy and CDN

The bundled `deploy/Caddyfile` sets a 64 KiB header limit, a 10-second header
timeout, a 256 MiB absolute body limit, and overwrites forwarded addresses from
the TCP peer. It is therefore a direct-to-Caddy baseline. Do not use its
`{remote_host}` forwarding lines unchanged behind a CDN: all clients would be
attributed to a CDN egress address, collapsing rejection aggregation and the
invalid-auth limiter onto unrelated users.

The bundled Caddy configuration leaves `flush_interval` unset so Caddy can
automatically flush `text/event-stream` responses while still propagating
client cancellation upstream. Do not set it globally: positive values can add
streaming latency, while Caddy 2.6.2's special `-1` mode also causes
reverse-proxied requests to continue after clients disconnect. The
configuration uses an explicit response content-type list for compression. Do
not replace that list with `text/*` or the shorthand `encode gzip zstd`: both
match `text/event-stream` and can buffer SSE until the response ends. Keep
streaming responses uncompressed while retaining compression for the web UI,
JSON, and static assets.

For a CDN deployment, first firewall the origin so only current CDN egress
CIDRs can connect. Then configure those exact ranges as Caddy trusted proxies
and derive upstream headers from Caddy's parsed `{client_ip}`. For example:

```caddyfile
{
	servers {
		trusted_proxies static 192.0.2.0/24 2001:db8:1234::/48
		trusted_proxies_strict
		client_ip_headers CF-Connecting-IP X-Forwarded-For
	}
}

api.example.com {
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Real-IP {client_ip}
		header_up X-Forwarded-For {client_ip}
	}
}
```

Replace the documentation ranges with the CDN's published, automatically
maintained egress ranges. `CF-Connecting-IP` is safe here only because direct
origin access is blocked and Caddy trusts only those TCP peers. Configure
Sub2API `server.trusted_proxies` with the Caddy address/private subnet so the
application accepts only Caddy's rewritten headers.

Caddy core does not provide a general request-rate limiter; use a trusted
CDN/WAF, a supported rate-limit module, or host firewall controls.

At a CDN/WAF, configure connection limits, header/body limits, bot challenges,
and per-IP/ASN rates before traffic reaches the origin. Allow origin ingress
only from CDN egress CIDRs or a private load balancer. Keep the application port
off the public Internet.

## DDoS boundary

Application checks reduce amplification after a connection reaches Go. They
cannot absorb volumetric attacks, TLS floods, bandwidth saturation, or a large
distributed source set. Those require upstream network capacity, CDN/WAF
filtering, provider firewall rules, and origin isolation. Avoid high-cardinality
metrics or per-request database security logs during rejection storms.
