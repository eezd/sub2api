# 降智检测代码审查修复计划

## Context

这份计划修复 ModelTrace / SVG 动画降智检测审查中确认的 1 个高危问题和 5 个中等问题。修复都限定在本次新增功能内：

- 不改数据库结构和已有接口的请求格式。
- 上游已有文件 `account_test_service.go`、`AccountTestModal.vue` 只加最少的钩子，减少以后同步上游时的冲突。

## Approach

### 高：SVG 预览安全策略固定放在文档最前面

文件：`frontend/src/components/admin/account/SvgAnimationTestPanel.vue`

- 删掉 `addPreviewPolicy` 里靠正则找 `<head>` / `<html>` 的分支。`buildPreviewDocument` 统一返回：
  ```ts
  `<!doctype html><meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">${stripUnsafeMarkup(markup)}`
  ```
- HTML 解析时，这个 `<meta>` 会进入浏览器自动创建的 head，所以策略一定生效，而且排在模型输出的任何资源之前。模型输出里自带的 `<!doctype>`、`<html>`、`<head>` 都变成可忽略的解析错误，`<style>` 和 `<svg>` 仍能正常渲染。
- 实时检测和历史回放都走 `buildPreviewDocument`，这一处修改同时覆盖两条路径。
- 保留 `sandbox=""`、`referrerpolicy="no-referrer"` 和 `stripUnsafeMarkup`。`stripUnsafeMarkup` 只是额外一层防护，安全不依赖它：多个 CSP 同时存在时取交集，模型输出不可能放宽限制；自动刷新和跳转已被 `sandbox=""` 禁止。

### 中 1：取消或断开连接时也保存记录

文件：`backend/internal/service/account_degradation_check_service.go`、`model_trace_service.go`

- `persistDegradationCheck` 内部改用独立的写库上下文：
  ```go
  persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
  defer cancel()
  ```
  这样所有写入路径都不再受请求取消影响，包括成功记录、失败记录和 `persistModelTraceResult`。
- 调用上游仍使用请求上下文。管理员取消后要立即停止花费上游额度，这一点不变。
- ModelTrace 循环：在每次调用上游之前和之后都检查 `c.Request.Context().Err()`。发现已取消时，写一条错误记录 `检测已取消（已尝试 N/6 次）`，然后返回。已收集的部分序列不做评分。
- SVG：调用上游返回后，如果请求已取消，写一条错误记录 `检测已取消`（`output_text` 保留已收到的部分），不再写上游返回的取消类报错文本。

### 中 2：SVG 错误信息清理和截断

文件：`model_trace_service.go`、`account_degradation_check_service.go`

- 把 `compactModelTraceProbeError` 改名为 `compactDegradationCheckError`，两种检测共用。
- 截断改为按字符（rune）进行，上限 500 个字符，避免截在多字节中文中间产生非法 UTF-8。仓库里没有可以直接复用的按字符截断函数（`truncateAuditExtraString` 在 middleware 包里），就在本文件内实现：`[]rune` 切片后加 `…`。
- `TestSVGAnimation` 在记录失败和推送 `error` 事件之前，都先对 `probeError` 调用该函数。

### 中 3：补齐 ModelTrace 漏掉的失败记录

文件：`model_trace_service.go:59-64`

- 在"账号类型不支持"和"图像模型"这两个分支里，先调用 `recordDegradationCheckFailure(..., AccountDegradationCheckModelTrace, requestedModel, "", "", message)`，再调用 `sendErrorAndEnd`，和 SVG 的处理方式一致。

### 中 4：旧检测不能改写新检测的状态

文件：`SvgAnimationTestPanel.vue`、`AccountTestModal.vue`

- 检测进行中禁用历史按钮：
  - SVG：`:disabled="item.status !== 'success' || status === 'running'"`
  - ModelTrace：`:disabled="item.status !== 'success' || modelTraceStatus === 'running'"`
  - `showHistoryResult` / `showModelTraceHistoryResult` 入口也加同样的判断并直接返回。
- 请求中断后的处理只允许当前这次运行执行：
  - SVG `catch`：先判断 `if (abortController !== controller) return`，通过后才能设为 `idle` 或 `fail`。
  - ModelTrace `catch`：同样先判断 `if (modelTraceAbortController !== controller) return`。
  - `reset()` / `abortModelTrace()` 已经先把控制器置空再中断请求，所以旧请求进入 `catch` 时这个判断一定为真，从而直接退出。

### 中 5：截断或被过滤的回答不算成功

文件：`account_test_service.go`（上游文件，只加钩子）、`account_degradation_check_service.go`、`model_trace_service.go`

- 在本功能新增的文件里定义：
  ```go
  const accountTestStopReasonContextKey = "account_test_stop_reason"
  func recordAccountTestStopReason(c *gin.Context, reason string)
  ```
- 在上游的流处理函数里各加一行调用，普通连接测试的行为不变：
  - `processClaudeStream`：新增 `message_delta` 分支，读取 `delta.stop_reason`。
  - `processOpenAIChatCompletionsStream`：已经在读 `finish_reason` 的地方顺手记录。
  - `processOpenAIStream`：新增 `response.incomplete` 分支，记录 `response.incomplete_details.reason`。现在这种情况会以 `Stream ended before response.completed` 失败，加上原因后报错更清楚。
- `runDegradationCheckProbe` 从测试用上下文里读出结束原因。遇到 `max_tokens`、`refusal`、`length`、`content_filter`、`max_output_tokens` 时，返回错误 `回答未正常完成（<reason>），本次不计入`，和 Python 参考实现 `enrollment.py:277-288` 的判断一致。
- ModelTrace：这类回答算作一次失败尝试，继续用剩下的尝试次数。
- SVG：记录为错误，`output_text` 保留截断的原文供排查，界面不回放。
- SVG 的 Claude 输出上限仍保持 4096，不改 `createTestPayload`（原因见 Assumptions）。

## Files

| 文件 | 改动 |
|---|---|
| `frontend/src/components/admin/account/SvgAnimationTestPanel.vue` | CSP 放到最前、运行中禁用历史、`catch` 判断归属 |
| `frontend/src/components/admin/account/AccountTestModal.vue` | 运行中禁用历史、`catch` 判断归属 |
| `backend/internal/service/account_degradation_check_service.go` | 独立写库上下文、SVG 错误清理、取消记录、结束原因辅助函数 |
| `backend/internal/service/model_trace_service.go` | 取消记录、补齐两种失败记录、按字符截断、拒绝截断的回答 |
| `backend/internal/service/account_test_service.go` | 三个流处理函数各加一行结束原因记录 |
| 测试文件 | 见 Verification |
| `README.md` / `README_CN.md` / `README_JA.md` | "成功与失败记录"改为"成功、失败与取消记录"；截断的回答不计入 |

## Verification

### 后端单元测试（`-tags=unit`）

在 `account_degradation_check_service_test.go` / `model_trace_service_test.go` 中补充：

1. 取消：upstream 假对象在第一次调用时取消请求上下文 → 只写一条 `status=error`、消息包含"已取消"的记录。内存仓库的 `Create` 断言收到的 `ctx.Err() == nil`，证明写库没有被取消。SVG 同样覆盖。
2. 错误清理：上游返回 400，内容包含 `?key=secret` 以及超过 500 个字符的中文 → 保存的 `error_message` 里密钥已遮盖，长度不超过 501 个字符，并且 `utf8.ValidString` 为真。
3. ModelTrace 对不支持的账号类型、对图像模型 → 各写一条错误记录。
4. 截断：Claude 流带 `message_delta.stop_reason=max_tokens` → SVG 记为错误；Chat Completions 流 `finish_reason=length` → ModelTrace 本次尝试被拒绝，继续下一次。
5. 普通连接测试回归：`go test -tags=unit ./internal/service ./internal/handler/admin ./internal/server/routes ./internal/repository ./cmd/server`。

### 前端测试（vitest）

1. CSP：对四种输入做检查，每种都用 `DOMParser` 解析生成的 srcdoc，断言 `doc.head.firstElementChild` 是 CSP meta、body 里没有 CSP meta。四种输入是：
   - `<head>` 出现在 `<img>` 之后
   - `<head>` 只出现在注释里
   - 纯 `<svg>`
   - 标准的完整文档
2. 并发：A 检测正在运行时历史按钮处于禁用状态。切换账号会中断 A，随后启动 B；A 的中断处理结束后，状态仍然是 `running`，并且 B 的完成事件能正常把状态设为 `success`。两个面板各测一次。
3. `vue-tsc --noEmit`、eslint，以及 i18n key 完整性测试。

### 实际页面烟测（使用现有预览环境 :15173 / :18080 和一次性的模拟上游）

在 `/tmp/sub2api-modeltrace-preview-upstream.go` 中增加 `/__mode` 控制接口，并在 :19191 启动一个一次性的监听服务，用于记录收到的请求。然后依次验证：

1. SVG 返回 `<img src=http://<host>:19191/leak>` 放在 `<head>` 前面，以及 `<head>` 在注释里这两种情况。实时检测和回放历史时，监听服务都收不到任何请求，动画照常显示。
2. 开始 ModelTrace 后立即关闭弹窗 → 历史里出现"已取消（已尝试 N/6 次）"。
3. 模拟上游返回 `finish_reason=length` → ModelTrace 历史里显示失败原因，SVG 显示"回答未正常完成"。
4. 检测进行中时，历史按钮显示为禁用，点击没有反应。

## Assumptions & contingencies

- Claude 的 SVG 输出上限保持 4096，截断的回答明确记为失败，而不是提高上限。
  - 提高到 16384 能减少截断，但 Claude 3.x 等旧模型的输出上限较小，会直接返回 400。
  - 按模型区分上限需要一张模型能力表，超出这次修复的范围。
  - 如果实际截断频繁，再单独评估是否给 SVG 设置更高的上限。
- `<link rel=dns-prefetch>` 只会触发 DNS 查询，不受 CSP 控制，属于已知的残余风险，不会产生 HTTP 请求。
- 取消后前端会立刻重新加载历史，可能比后端写入"已取消"记录早一点。下一次打开弹窗或完成下一次检测时历史会刷新，不额外加轮询。
- 不在这次范围内（审查中的低优先级项）：
  - 删掉 `removeModelTraceNuisance` 里跳过长度不一致轴的 `continue`，并在 `validateModelTraceBank` 中检查长度。
  - 数字解析支持全角数字。
  - 把 ModelTrace 面板从 `AccountTestModal.vue` 拆成独立组件。
