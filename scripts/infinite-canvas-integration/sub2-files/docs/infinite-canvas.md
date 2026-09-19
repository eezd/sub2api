# Infinite Canvas 集成与运维

Sub2API 默认将 `basketikun/infinite-canvas` 构建为同源子应用 `/canvas-app/`，用户入口是 `/workspace/canvas`。Canvas 产物直接嵌入发布镜像和二进制；如需使用外部部署，可通过服务端 `INFINITE_CANVAS_URL` 配置完整 HTTP(S) 地址，前端会从公开运行时设置读取该地址，无需重新构建。

## 获取源码

必须初始化子模块：

```bash
git clone --recurse-submodules https://github.com/eezd/sub2api.git
cd sub2api
git submodule update --init --recursive
```

## Docker 发布

```bash
docker build -t eezd-sub2api:canvas .
docker run --rm -p 8080:8080 eezd-sub2api:canvas
```

发布后检查：

```text
GET /health
GET /canvas-app/
GET /canvas-app/version.txt
```

登录后访问：

```text
/workspace/canvas
```

页面读取当前用户的全部 active API Key。默认同源；配置外部 Canvas 时，`postMessage` 只向该地址的 origin 发送，并只接受来自同一 origin 和目标 iframe 的消息。API Key 不会放进 URL。外部 Canvas 必须实现相同的 bridge 消息协议。

## 第一阶段 Codex Agent 连接

- 默认入口：`/canvas-app/canvas?mode=new`；配置 `INFINITE_CANVAS_URL` 后，入口基于该地址生成。
- 页面里增加“连接 Codex”帮助卡片。
- 复制命令：`npx -y @basketikun/canvas-agent`
- 自动检测：`http://127.0.0.1:17371/config`
- 检测失败时显示“下载/启动 Agent”说明。
- 不把 token 放在公共 URL 中。

## 上游升级

### Sub2API

升级 Sub2API 上游后，在生成的工作树中运行：

```bash
node scripts/apply-sub2-infinite-canvas-integration.mjs --root <generated-root>
```

适配器遇到 marker 不匹配时应立即失败；先根据报错更新适配器和契约测试，再继续升级。

### Infinite Canvas

1. 更新 Git submodule 指针。
2. 在临时工作树中应用 `scripts/apply-infinite-canvas-patches.mjs`。
3. 运行 Canvas typecheck 和生产构建。
4. 通过代码审查确认上游变更、许可证和适配器契约，再更新主仓库中的 submodule 指针。

补丁不直接写进上游子模块，避免后续升级时产生长期分叉。

## 故障定位

### 页面提示连接失败

检查发布产物是否包含：

```text
backend/internal/web/dist/canvas-app/index.html
```

同时确认 `/canvas-app/` 返回 Canvas HTML，而不是 Sub2 主站 HTML。

### Canvas 打开但没有模型

确认所选 API Key：

- 状态为 active。
- 能访问 `/v1/models`。
- 所属分组包含图片、视频或文本模型。
- 没有被额度、到期时间或 IP 规则阻止。

### 更新后适配器失败

不要跳过失败门禁。先对新上游提交运行：

```bash
node scripts/apply-infinite-canvas-patches.mjs --root integrations/infinite-canvas
```

根据报出的缺失 marker 更新适配器和契约测试，再合并升级 PR。

## 回滚

回滚包含 submodule 指针的主仓库提交，然后重新构建镜像。Canvas 的 localStorage 和 IndexedDB 数据不会被回滚命令主动删除。

## 许可证

Infinite Canvas 使用 AGPL-3.0。二次发布时保留其作者信息、许可证和对应修改源码。Sub2API 的适配代码与 Infinite Canvas 上游源码通过 Git submodule 和构建时适配器保持边界。
