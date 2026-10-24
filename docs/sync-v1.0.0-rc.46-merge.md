# dev 同步上游 v1.0.0-rc.46 合并记录

- 日期：2026-10-24
- 操作：`git merge --no-ff v1.0.0-rc.46` 合入本地 `dev`
- 上游 tag：`v1.0.0-rc.46` → `7d8b91268`（2026-10-22，"fix: upgrade Go to 1.26.10 for CVE-2026-111943 (#7942)"）
- 合并前 `dev`：`d6126ad36`
- 合并基点（merge-base）：`789c97019`（2026-09-28，"fix(claude): preserve auto mode safeguards in native requests (#7598)"）
- 范围：上游新增 140 个提交；dev 自有提交 16 个（15 个功能提交 + 1 个文档提交，见 `docs/dev-custom-commits.md`）
- 验证：仅执行编译（`go build ./...` 与 `cd relaykit && GOWORK=off go build ./...` 均通过）；**未运行单元测试和前端构建**

## 一、合并策略

- 用 merge，不用 rebase：`dev` 已推送到远端，rebase 需要强推，并且要逐个提交重复解决冲突。
- 解决冲突的原则：**接受上游的结构性改动（函数签名、日志写法），保留 dev 的行为改动**（错误信息脱敏、Claude 归一化）。

## 二、文本冲突（3 个文件，5 处）

### 1. `controller/relay.go` — `getChannel` 重试选渠道失败

| 一方 | 内容 |
|------|------|
| dev | 详细信息（分组、模型、错误）写 `logger.LogWarn`，客户端只收到 `"The model is currently unavailable, please try again later"` |
| 上游 | 改为 i18n 文案 `MsgRelayRetryGetChannelFailed` / `MsgRelayRetryNoAvailableChannel`，**文案中包含分组名和模型名** |

**处理：保留 dev。** 上游的文案会把分组名暴露给客户端，和 dev 的"隐藏分组信息"目标冲突。

```go
if err != nil {
    logger.LogWarn(c, fmt.Sprintf("get channel failed for group %s model %s (retry): %s", selectGroup, info.OriginModelName, err.Error()))
    return nil, types.NewError(fmt.Errorf("The model is currently unavailable, please try again later"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
}
if channel == nil {
    logger.LogWarn(c, fmt.Sprintf("no available channel for group %s model %s (retry)", selectGroup, info.OriginModelName))
    return nil, types.NewError(fmt.Errorf("The model is currently unavailable, please try again later"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
}
```

影响：上游的 i18n key `relay.retry_get_channel_failed`、`relay.retry_no_available_channel` 在 Go 代码中不再被引用（定义还在 `i18n/keys.go`，不影响编译）。

### 2. `controller/relay.go` — `respondTaskError`

| 一方 | 内容 |
|------|------|
| dev | 429 **和 503** 统一返回 `"Overloaded"` |
| 上游 | 只处理 429，返回 i18n 文案 `MsgRelayGroupUpstreamSaturated`（"当前分组上游负载已饱和…"） |

**处理：保留 dev。**

```go
if taskErr.StatusCode == http.StatusTooManyRequests || taskErr.StatusCode == http.StatusServiceUnavailable {
    taskErr.Message = "Overloaded"
}
```

影响：i18n key `relay.group_upstream_saturated` 不再被引用。

### 3. `relay/channel/claude/relay-claude.go` — `HandleStreamResponseData`

| 一方 | 内容 |
|------|------|
| dev | 在 `countClaudeStreamBillableTools(c, info, ...)` 之前插入流式事件归一化 |
| 上游 | `countClaudeStreamBillableTools` 去掉了第一个参数 `c` |

**处理：两边合并。** 使用上游的新签名，同时保留 dev 的归一化调用。

```go
if shouldNormalizeBody(info) {
    data = normalizeClaudeStreamEvent(data, claudeResponse.Type)
}
countClaudeStreamBillableTools(info, &claudeResponse)
```

### 4–5. `relay/helper/valid_request.go` — `GetAndValidateRerankRequest` / `GetAndValidateEmbeddingRequest`

| 一方 | 内容 |
|------|------|
| dev | 错误经过 `sanitizeUnmarshalError(err)` 脱敏（去掉 Go 内部类型名） |
| 上游 | 日志改用 `common.LogText(...)` |

**处理：两边合并。** 日志使用上游写法，返回值保留 dev 的脱敏。

```go
logger.LogError(c, common.LogText("getAndValidateTextRequest failed: %s", err.Error()))
return nil, types.NewError(sanitizeUnmarshalError(err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
```

## 三、语义冲突（没有文本冲突，但自动合并后功能失效）

### `middleware/distributor.go` — 选渠道失败时隐藏分组名（dev 提交 `27b454a55`）

**问题**：上游把 distributor 的选渠道逻辑重构到了 `service/channel_select.go` 的 `SelectChannelForRequest`，返回 `ChannelSelectError{MessageID: MsgDistributorGetChannelFailed / MsgDistributorNoAvailableChannel, Params: {Group, Model, Error}}`，再由 distributor 统一用 i18n 渲染。dev 原来改动的代码段被上游整体替换，git 自动合并时没有报冲突，但**客户端会重新看到** `"分组 xxx 下模型 xxx 无可用渠道（distributor）"` 这类包含分组名的报错。

**处理**：在 `middleware/distributor.go` 中，渲染完 message 之后、返回给客户端之前，把这类错误替换为通用文案，原始信息写入 warn 日志：

```go
message := selectErr.Message
if selectErr.NoAvailableChannel {
    message = noAvailableChannelMessage(c, usingGroup, modelRequest.Model)
} else if selectErr.MessageID != "" {
    message = i18n.T(c, selectErr.MessageID, selectErr.Params)
}
if selectErr.NoAvailableChannel || selectErr.MessageID == i18n.MsgDistributorGetChannelFailed {
    logger.LogWarn(c, fmt.Sprintf("get channel failed (distributor): %s", message))
    message = "The model is currently unavailable, please try again later"
}
abortWithOpenAiMessage(c, selectErr.StatusCode, message, selectErr.Code)
```

覆盖范围：获取渠道出错、无可用渠道、渠道不满足过滤条件、任务插件无可用渠道（`noAvailableChannelMessage`）。
没有覆盖的情况：`strict_session_binding_unavailable`（上游新增的严格会话绑定错误）和其他带 MessageID 的错误，仍按上游原样返回。这些文案不包含分组名。

## 四、自动合并的文件（两边都改过，git 自动合并成功）

| 文件 | dev 改动 | 合并后检查结果 |
|------|----------|----------------|
| `model/ability.go`、`model/channel_cache.go` | 优先级取模循环 | 已保留 |
| `relay/channel/openai/relay-openai.go` | 流首包错误返回 502 | 已保留 |
| `relay/compatible_handler.go`、`relay/responses_handler.go` | `NormalizeClaudeError` 接入 | 已保留（`chat_completions_via_responses.go`、`claude_handler.go` 中的调用也都在） |
| `relay/helper/stream_scanner.go` | 开关打开时跳过 `copyCodexSSEHeaders` | 已保留 |
| `relaykit/dto/channel_settings.go` | `ForceClaudeFormat` / `NormalizeClaudeBody` / `NormalizeClaudeHeaders` | 已保留；relaykit 独立编译通过 |
| `service/error.go` | 429/503 改写为 `Overloaded` | 已保留 |
| `web/.../channel-mutate-drawer.tsx`、`channel-form.ts`、`en.json`、`zh.json` | 渠道开关 UI 与文案 | 自动合并；**未做前端构建验证** |

## 五、遗留事项（这次没有处理）

1. **单元测试没有运行**。已知 `setting/operation_setting/status_code_ranges_test.go` 中有 3 个用例在合并前就失败（dev 提交 `972d8b6d4` 让 504/524 可以重试，但测试没有同步）。合并后这个状态不变。
2. `setting/operation_setting/status_code_ranges.go` 第 18–20 行的注释还写着 "5xx(except 504/524)"，和 dev 的实际行为不一致。
3. 前端没有执行 `bun run build`。
4. 上游新增的 Claude 相关代码路径（比如新的 relay format 或 handler）是否都接入了 `NormalizeClaudeError` 和响应体归一化，没有逐一核对。
5. `controller/relay.go` 中的提示文案是硬编码的英文，没有走 i18n。这是 dev 原本就有的设计，这次没有改。

## 六、回滚

合并提交创建后，如果需要回滚：

```bash
git reset --hard d6126ad36   # 合并还没推送时
git revert -m 1 <merge-commit>  # 合并已经推送时
```
