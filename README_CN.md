<div align="center">

<img src="assets/logo.svg" alt="Sub2API Logo" width="128" />

# Sub2API — eezd fork

[![Release](https://img.shields.io/github/v/release/eezd/sub2api?include_prereleases&label=release)](https://github.com/eezd/sub2api/releases)
[![Go](https://img.shields.io/badge/Go-1.27.0-00ADD8.svg)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)

**面向订阅配额分发、多供应商路由和运维管理的 AI API 网关。**

[English](README.md) · 中文 · [日本語](README_JA.md)

</div>

> 本仓库是 [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) 的 `eezd/sub2api` fork。它持续同步上游，同时维护下方列出的 fork 功能。本项目不是上游官方发行版。

## 本 fork 新增内容

下表说明当前版本相对上游的重要差异。Sub2API 的核心能力仍然保留；这里重点列出由本 fork 维护的功能。

| Fork 新增能力 | 说明 | 文档 |
|---|---|---|
| **Infinite Canvas 工作区** | 在 `/workspace/canvas` 集成 Infinite Canvas，并支持通过 `INFINITE_CANVAS_URL` 使用外部部署。可连接本地 Codex Agent，API Key 不会写入 URL。 | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| **账号最近请求时间线** | 管理员账号列表新增“最近请求”列，展示最近 15 分钟内每个账号最多 5 条请求，包括成功/错误状态、耗时、首 Token、Token 数量、成本、倍率和详情。每轮按账号批量请求，避免逐账号轮询的 N+1 请求。 | [`POST /api/v1/admin/ops/requests/recent-by-account`](backend/internal/server/routes/admin.go) |
| **ModelTrace 模型降智检测** | 为 OpenAI 和 Anthropic 文本模型账号提供仅管理员可用的检测。管理员必须显式选择模型；服务端最多调用 6 次以取得 3 条有效的无语义数字序列，再与内置 GPT/Claude 参考库进行闭集统计归因。因输出上限、拒答或内容过滤而未正常完成的回答不计入。成功、失败与取消记录均按账号持久化。结果仅用于诊断，不是模型身份的确定性证明。 | [`POST /api/v1/admin/accounts/:id/model-trace`](backend/internal/server/routes/admin.go)<br>[历史查询接口](backend/internal/server/routes/admin.go) |
| **SVG 动画降智检测** | 在 ModelTrace 数字指纹题之外提供独立的单次可视化测试。管理员选择文本模型后，系统发送固定的“鹈鹕骑自行车”HTML/SVG 题目，并在禁用脚本的隔离沙箱中展示返回动画；内容安全策略固定放在模型输出之前，预览无法加载任何外部资源。被截断或过滤的回答记为失败。成功、失败与取消记录均会持久化，可从账号历史中重新播放旧动画。 | [`POST /api/v1/admin/accounts/:id/svg-animation-test`](backend/internal/server/routes/admin.go)<br>[历史查询接口](backend/internal/server/routes/admin.go) |
| **持久化批量降智检测** | 跨页选择账号后，使用批量操作栏的“降智检测”或“SVG 动画降智检测”，每批最多 1,000 个账号。必须为每个账号显式选择模型；平台填充只应用于自身目录支持该模型的账号，不支持或未选模型的账号跳过。服务端后台任务不受关闭弹窗、切换页面、刷新或退出登录影响；无需选择账号即可从“批量检测任务”重新进入。显式取消会停止排队及运行中的项目，保留已完成结果和账号历史。共享数据库的多实例最多同时执行两项，同一账号串行检测。中断项目不自动重放，待执行项目继续处理。检测会消耗上游额度。 | [`/api/v1/admin/accounts/degradation-check-batches`](backend/internal/server/routes/admin.go) |
| **Codex turn-state 门票** | 可选地为指定 OpenAI OAuth 模型获取并注入 `x-codex-turn-state`。支持后台开关、采集代理、TTL、模型范围和失败关闭策略。 | [`deploy/config.example.yaml`](deploy/config.example.yaml) 中的 `gateway.openai_codex_ticket` |
| **签名版 OpenAI Transport 插件** | 提供官方 `.s2plugin`，为 OpenAI OAuth 提供独立出站 HTTP/TLS 传输，支持 Node.js 24 风格 ClientHello、账号代理继承、连接限制、超时、UI 配置、签名校验和灰度发布。 | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| **TLS 指纹配置** | 为兼容的出站链路提供托管 TLS 指纹配置，限制 ClientHello 字段，修改配置需要二次认证，并支持直连、HTTP CONNECT 和 SOCKS5H 代理。 | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md) 和 `backend/pkg/pluginapi/docs/` |
| **更严格的出站 URL 安全策略** | 新安装默认启用 HTTPS 出站校验，并拒绝私有、回环、链路本地和未指定地址。重定向和响应中提供的 URL 也会单独校验。 | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| **Fork 发布版本线** | 使用 `vX.Y.Z-custom.N` 发布版本、签名插件包和完整版本号 GHCR 镜像。当前版本为 [`v0.2.9-custom.1`](https://github.com/eezd/sub2api/releases/tag/v0.2.9-custom.1)。 | [Release workflow](.github/workflows/release.yml) |

### 官方插件兼容规则

官方插件与其测试通过的 fork 版本一起发布。例如：

```text
Sub2API: 0.2.9-custom.1
Plugin:  0.2.9-custom.1
Requires: >=0.2.9 <0.3.0
```

请从同一个 GitHub Release 下载匹配的 `.s2plugin`。生产环境应保持 `plugins.allow_unsigned: false`；官方包由宿主内置发布者公钥校验。

## 保留的上游能力

本 fork 继续包含上游平台能力，包括：

- OAuth 和 API Key 多账号管理。
- API Key 分发、分组、粘性会话、账号调度、故障转移、并发限制和速率限制。
- Token 级用量统计、模型定价、计费、订阅和内置支付服务商。
- 当前版本提供的 OpenAI 兼容、Anthropic 兼容、Gemini、Grok/xAI、Antigravity 等供应商接入。
- Composite Groups：根据模型在多个供应商之间路由（[运维指南](docs/COMPOSITE_GROUPS.md)）。
- 同步和异步图片任务、批量图片处理，以及 OpenAI Responses WebSocket 入口限制。
- 管理监控、用量报表、备份、Prompt Audit、安全设置和外部管理后台集成。
- 面向个人或内部团队的简易模式。

上游历史和基础架构请参阅 [上游项目](https://github.com/Wei-Shaw/sub2api)。Fork 特有行为以本文和上方链接的文档为准。

## 重要提醒

- 使用 OAuth 账号、订阅配额或 API 中转功能前，请阅读所有上游服务商的服务条款。
- 使用本项目必须遵守适用法律、服务商合同、隐私要求、支付规则和网络访问限制。
- 本软件仅供技术学习和研究使用。部署者对账号、数据、用户、计费、内容、合规和运行安全承担全部责任。
- 本项目不授权或背书任何基于本仓库的部署、托管服务、付费套餐、中转服务或商业活动。运营公开或商业实例前，请阅读 [`docs/legal/admin-compliance.en.md`](docs/legal/admin-compliance.en.md)。

## 快速开始

### Docker Compose

Docker Compose 适合开发和自托管。仓库中的 Compose 文件默认仍使用上游镜像名称；使用本 fork 时，请替换为 fork Release 对应的 GHCR 镜像：

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api/deploy
cp .env.example .env
chmod 600 .env

# 在 .env 中设置强密码 POSTGRES_PASSWORD、JWT_SECRET 和 TOTP_ENCRYPTION_KEY。
# 将应用镜像替换为 fork 的固定版本：
sed -i 's#weishaw/sub2api:latest#ghcr.io/eezd/sub2api:0.2.9-custom.1#' docker-compose.local.yml

mkdir -p data postgres_data redis_data
docker compose -f docker-compose.local.yml up -d
docker compose -f docker-compose.local.yml logs -f sub2api
```

打开 `http://你的服务器IP:8080`。`AUTO_SETUP=true` 时，容器会执行数据库迁移并创建初始管理员账号。如果没有设置 `ADMIN_PASSWORD`，请从应用日志中读取自动生成的密码。

生产环境请固定使用 fork 的具体版本，不要使用 `latest`。本地目录版 Compose 更适合生产，因为 `data/`、`postgres_data/` 和 `redis_data/` 可以一起备份和迁移。

Compose 版本、恢复行为、Apple `container`、迁移和运维命令见 [`deploy/README.md`](deploy/README.md)。

### 预编译二进制

从 [fork Releases](https://github.com/eezd/sub2api/releases) 下载对应平台压缩包，并准备 PostgreSQL 15+ 与 Redis 7+。没有配置文件时，启动服务会进入设置向导并创建第一个管理员。

当前 Release 包含：

- Linux amd64、arm64
- macOS amd64、arm64
- Windows amd64

### 源码编译

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api

corepack enable
cd frontend
pnpm install
pnpm run build

cd ../backend
VERSION="$(./scripts/resolve-version.sh)"
go build -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server
./sub2api
```

`embed` 构建标签会把前端嵌入二进制。开发环境下可分别运行后端和前端，详见 [`DEV_GUIDE.md`](DEV_GUIDE.md)。

## 关键配置

### 安全的出站默认值

新安装应保持出站 URL 边界开启：

```yaml
security:
  url_allowlist:
    enabled: true
    upstream_hosts:
      - api.openai.com
      - api.anthropic.com
    allow_private_hosts: false
    allow_insecure_http: false
```

将供应商域名添加到 `upstream_hosts`，不要为了添加供应商而关闭边界。跨源重定向会在转发凭据前被拒绝；响应中提供的 URL 仅允许访问公开地址，并会校验每一跳重定向。

仅可在隔离的开发环境中使用以下弱化配置：

```yaml
security:
  url_allowlist:
    enabled: false
    allow_private_hosts: true
    allow_insecure_http: true
```

关闭这些检查可能暴露凭据和内部服务。如因兼容性必须关闭，请在网络边界实施等效的出站控制。

### OpenAI WebSocket 回退

如果代理反复重连 OpenAI Responses WebSocket，可强制上游使用 HTTP/SSE，而不改变客户端协议：

```yaml
gateway:
  openai_ws:
    force_http: true
```

Compose 可设置 `GATEWAY_OPENAI_WS_FORCE_HTTP=true`。完整 WebSocket 配置见 [`deploy/config.example.yaml`](deploy/config.example.yaml)。

### Codex ticket 采集

该功能默认关闭。仅在确有需要的账号和模型上启用：

```yaml
gateway:
  openai_codex_ticket:
    enabled: true
    harvest_proxy_url: socks5h://user:password@proxy.example:1080
    models:
      - gpt-6-astra
      - gpt-5.6-sol
    fail_closed: true
```

门票由系统管理。不要把门票写入账号编辑请求或用户提供的账号元数据，并将采集代理凭据视为敏感信息。

## 安装 OpenAI Transport 插件

1. 从匹配的 fork Release 下载 `sub2api-openai-transport-<version>.s2plugin`。
2. 登录管理后台，打开“插件”，上传包并确认签名和兼容性。
3. 配置并测试插件后再启用。
4. 使用较低灰度比例开始，观察插件状态、请求成功率、延迟和日志。
5. 禁用绑定即可让 OpenAI OAuth 账号回到核心传输路径。

插件不负责账号选择、OAuth 刷新、SSE 解析、重试或计费。不要解压或修改已签名的包。完整安装、升级、回滚、配置和源码构建说明见 [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md)。

## Infinite Canvas

Fork 在以下路径集成 Canvas：

```text
/workspace/canvas
```

默认同源部署从 `/canvas-app/` 提供内嵌应用。设置 `INFINITE_CANVAS_URL` 可使用外部 Canvas；外部应用必须实现文档规定的 bridge。API Key 通过认证 bridge 传递，不会放入公开 URL。

从 Canvas 工作区连接 Codex Agent：

```bash
npx -y @basketikun/canvas-agent
```

部署、bridge 安全、外部托管和故障排查见 [`docs/infinite-canvas.md`](docs/infinite-canvas.md)。

## 管理员账号最近请求

管理员账号列表的“最近请求”列会在列可见时启用。它通过一次批量请求刷新当前页，显示每个账号最近 15 分钟内最多 5 条请求，并提供请求 ID、模型、状态、时间、Token、成本和账号倍率详情。

该功能刻意避免旧版逐账号轮询模式。接口接收当前可见账号 ID，并返回分组结果：

```http
POST /api/v1/admin/ops/requests/recent-by-account
Content-Type: application/json

{"account_ids":[101,102,103]}
```

请求计时明细仅用于诊断，读取窗口为 30 天（包含截止边界）。即使物理清理尚未完成，过期明细也不会返回；每小时清理每批最多 10,000 行，共用五秒截止时间。下游断连与上游完成分别记录：即使上游随后失败，也保留已获取的 usage 和断连状态，不会因此再次请求模型。成功完成流在关闭过程中产生的取消或管道关闭，不归因为上游传输失败；Read 在 Close 开始前返回的错误仍会记录。OpenAI 透传保留请求的服务层级和推理强度，交由现有计费规则处理。

上游余额探测响应上限为 256 KiB；计费声明仍使用独立的 64 KiB 上限。余额响应超限时探测失败，保留上次成功余额。

Mihomo 节点固定端口持久化、单调分配且不复用。热重载仅预检新增端口，但在保存前验证所有已配置节点端口的 SOCKS 监听就绪，包括禁用/恢复节点时重建的已有监听。重载或就绪验证失败时，使用独立且有时限的上下文恢复并验证旧配置；无法验证恢复成功则停止托管内核，不会宣称旧配置仍生效。节点监听仅支持 TCP（HTTP/SOCKS5 CONNECT，`udp: false`），UDP 端口被占用时不会再残留半绑定的监听。

## Nginx 与反向代理

使用 Nginx 代理 Codex 或其他包含下划线请求头的客户端时，在 `http` 块加入：

```nginx
underscores_in_headers on;
```

同时只将直接连接 Sub2API 的代理 CIDR 配置到 `server.trusted_proxies`。信任 forwarded client-IP 请求头前，请阅读 [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md)。

## 文档索引

| 主题 | 文档 |
|---|---|
| 部署与升级 | [`deploy/README.md`](deploy/README.md) |
| 支付配置 | [`docs/PAYMENT_CN.md`](docs/PAYMENT_CN.md) |
| Composite Groups | [`docs/COMPOSITE_GROUPS.md`](docs/COMPOSITE_GROUPS.md) |
| 异步图片任务 | [`docs/ASYNC_IMAGE_TASKS.md`](docs/ASYNC_IMAGE_TASKS.md) |
| 批量图片处理 | [`docs/BATCH_IMAGE_MVP.md`](docs/BATCH_IMAGE_MVP.md) |
| OpenAI Transport 安装 | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| 插件开发与包格式 | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md)、[`backend/pkg/pluginapi/docs/`](backend/pkg/pluginapi/docs/) |
| Infinite Canvas | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| 边缘与代理安全 | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| 开发指南 | [`DEV_GUIDE.md`](DEV_GUIDE.md) |

## 项目结构

```text
sub2api/
├── backend/                  # Go 网关、服务、仓储和迁移
├── frontend/                 # Vue 管理端和用户界面
├── plugins/openai-transport/ # 官方 OpenAI Transport 插件 UI/运行时源码
├── integrations/infinite-canvas/ # Canvas 子模块
├── deploy/                   # Compose、二进制、Apple container 和配置
├── docs/                     # 运维、安全、支付和插件文档
└── scripts/                  # 构建和集成辅助脚本
```

## 许可证

本项目使用 [GNU Lesser General Public License v3.0 或更高版本](LICENSE) 发布。

Fork 保留上游许可证和版权声明。Fork 特有代码与文档遵循仓库适用许可证，详见 [`LICENSE`](LICENSE) 和 [`CLA.md`](CLA.md)。

<div align="center">

**如果本 fork 对你有帮助，欢迎 Star 并提交可复现的问题。**

</div>
