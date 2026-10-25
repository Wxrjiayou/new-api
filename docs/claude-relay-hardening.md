# Claude 中转链路加固：实施记录

- 分支：`dev`（基于合并 `v1.0.0-rc.46` 之后的 `539f711fc`）
- 依据：`docs/newapi-fork-messages-vs-lt(2).md` 中与 LianTong（LT）的对比结论
- 范围：第 1、2、3、4、5、6、8、10、11 项（第 7、9、12、13 项按决定不做）
- 共 10 个提交，每项单独提交并附带回归测试

| 提交 | 项 | 内容 |
|---|---|---|
| `4cd1be055` | — | 修正合并后就已失败的测试，使其符合 dev 的既有行为 |
| `f57e4c469` | 1 | 已向客户端输出内容后禁止换渠道重试 |
| `112cae45f` | 3 | Claude 错误 `error.type` 不再出现 `"<nil>"` |
| `fbca34724` | 11 | 敏感词命中返回 400，保留命中词，不重试 |
| `f90c318e8` | 6 | 补中文欠费关键词和"模型未定价"上游特征 |
| `5cd76a0dd` | 2 | 流中途报错按已收 usage 计费，不再全额退款 |
| `239efc9f7` | 4 | Claude 流被截断时补发终止帧 |
| `5866009ca` | 5 | 客户端在首个事件前断开时，再读上游最多 3 秒拿 usage |
| `353b47db3` | 10 | 首字节超时 10 分钟，所有尝试共用 20 分钟总预算 |
| `9feb3530f` | 8 | 非流式空 200 视为上游故障，换渠道重试 |

---

## 0. 修正合并后失败的测试 — `4cd1be055`

**问题**：合并 `v1.0.0-rc.46` 之后，有 4 个测试失败。它们断言的是上游的旧行为，而 dev 已经有意改掉了这些行为：

- `setting/operation_setting/status_code_ranges_test.go`、`service/relay_error_test.go`：仍然断言 504/524 不重试。dev 提交 `972d8b6d4` 已经让 504/524 可以重试。
- `middleware/distributor_test.go`：仍然断言"无可用渠道"的报错里带分组名。dev 的设计是只给客户端通用文案，分组名只写日志。

**修改**：断言改为 dev 的实际行为。分组名改成断言"出现在日志中、不出现在响应中"，原有的"插件名不泄露"等断言保留。`status_code_ranges.go` 里过时的注释也一并修正。

---

## 1. 已输出内容后禁止换渠道重试 — `f57e4c469`

### 处理的问题

流式响应是边收边转发的。如果渠道 A 已经把 `message_start` 和部分文字发给了客户端，再出错时重试循环会换到渠道 B，B 的完整响应会接在同一个 HTTP 响应后面：

```
message_start(A) → "你好，我是" → message_start(B) → "你好，我是Claude…" → message_stop
```

客户端收到两个 `message_start`，内容重复，Claude Code 等客户端会解析失败。改之前，重试判断 `DecideRelayRetry` 完全不检查响应是否已经写出。

### 为什么这样改

- 判断放在 `DecideRelayRetry` 的最前面：这是唯一的重试决策点，所有格式（Claude、OpenAI、Gemini）都经过它，改一处就能全部生效。
- 不能直接用 `c.Writer.Written()`：开启保活后，网关会先发 `: PING` 注释。如果按"写过字节"判断，只要发过一次 ping 就永远不能重试，保活就把重试能力废掉了。所以 ping 的字节单独计数，用"总写出字节数 − ping 字节数"判断有没有输出过真正的内容。
- 计数器放在 gin 上下文里，并在启动保活时先创建：保活协程用的是 `c.Copy()` 出来的上下文，提前创建可以保证两边拿到的是同一个计数器指针。

### 核心代码

`common/gin.go`：

```go
// KeepAliveBytes returns the shared ping byte counter, creating it on first use.
// Create it before gin.Context.Copy so copies share the same counter.
func KeepAliveBytes(c *gin.Context) *atomic.Int64 { ... }

// ResponseBodyStarted reports whether the client received response bytes other
// than keep-alive pings.
func ResponseBodyStarted(c *gin.Context) bool {
	if c == nil || c.Writer == nil || !c.Writer.Written() {
		return false
	}
	counter, _ := GetContextKeyType[*atomic.Int64](c, constant.ContextKeyKeepAliveBytes)
	if counter == nil {
		return c.Writer.Size() > 0
	}
	return int64(c.Writer.Size()) > counter.Load()
}
```

`relay/helper/common.go`（`PingData` 记录 ping 字节）：

```go
written, err := c.Writer.Write([]byte(": PING\n\n"))
common.KeepAliveBytes(c).Add(int64(written))
```

`service/relay_error.go`（`DecideRelayRetry` 开头）：

```go
if common.ResponseBodyStarted(c) {
	return PolicyDecision{Action: "stop", Reason: "response_already_written", Source: "system"}
}
```

这个判断排在 `channel_error`（渠道错误一律重试）之前，所以即使是渠道错误，已经输出过内容也不会再重试。

### 测试

`service/relay_error_test.go` → `TestDecideRelayRetryStopsAfterResponseBodyStarted`，覆盖 5 种情况：什么都没写（重试）、只写了 ping（重试）、ping 之后写了数据（停止）、写了数据（停止）、写了数据后又出现渠道错误（停止）。

---

## 3. Claude 错误类型不再是 `<nil>` — `112cae45f`

### 处理的问题

上游错误经过 OpenAI 错误结构中转时 `Code` 为 nil，`ToClaudeError` 用 `fmt.Sprintf("%v", openAIError.Code)` 生成 `type`，结果是字符串 `"<nil>"`。本地产生的错误则会得到 `new_api_error`、`do_request_failed` 这类 Anthropic 没有定义的类型。Claude Code 和 Anthropic SDK 是按 `error.type` 判断要不要重试的（例如 `overloaded_error`），类型不对，客户端就不会按过载处理。

### 为什么这样改

在 `ToClaudeError` 的出口统一兜底：如果算出来的 `type` 不在 Anthropic 文档定义的集合里，就按 HTTP 状态码映射。这样无论错误来自哪个分支、哪个渠道，客户端拿到的都是合法类型；上游本来就带了合法类型的（如原样透传的 `overloaded_error`），保持不变。

### 核心代码

`relaykit/types/error.go`：

```go
if _, ok := claudeErrorTypes[result.Type]; !ok {
	result.Type = claudeErrorTypeForStatus(e.StatusCode)
}
```

映射关系：400→`invalid_request_error`、401→`authentication_error`、402→`billing_error`、403→`permission_error`、404→`not_found_error`、413→`request_too_large`、429→`rate_limit_error`、504→`timeout_error`、503/529→`overloaded_error`、其他→`api_error`。

`relaykit` 是独立模块，已用 `cd relaykit && GOWORK=off go build ./...` 和 `GOWORK=off go test ./...` 验证。

### 测试

新增 `relaykit/types/error_test.go` → `TestToClaudeErrorUsesDocumentedTypes`，覆盖：合法类型保留、空类型按 529 映射、OpenAI 错误无 code、OpenAI 错误带非 Claude 的 code、本地 400/401/413/500 错误。

---

## 11. 敏感词命中返回 400 — `fbca34724`

### 处理的问题

命中敏感词时用 `types.NewError` 返回，默认状态码 500。这是客户端自己的请求有问题，返回 500 会让客户端和 SDK 误以为是服务端故障，并按 5xx 自动重试。

### 为什么这样改

按约定只改状态码和"不重试"标记，文案保持 `user sensitive words detected: <命中词>`，命中词继续返回给客户端；不加罚金。敏感词检查发生在预扣费之前（`PrepareRequestBilling`），被拒的请求不会产生预扣。

### 核心代码

`relay/request_billing.go`：

```go
return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeSensitiveWordsDetected,
	http.StatusBadRequest, types.ErrOptionWithSkipRetry())
```

Claude 格式下，第 3 项会把它映射为 `invalid_request_error`。

### 测试

新增 `relay/request_billing_test.go` → `TestPrepareRequestBillingRejectsSensitiveWordsWith400`：断言状态码 400、错误码不变、不重试、文案包含命中词、Claude 类型为 `invalid_request_error`、没有发生预扣（`info.Billing` 为 nil）。

---

## 6. 中文欠费关键词与"模型未定价"上游 — `f90c318e8`

### 处理的问题

dev 的 `claude_error_normalize.go` 已经照 LT 移植了英文的欠费、订阅限额关键词表，但缺两类：

1. 中文中转站的欠费文案（"余额不足""账户已欠费""请先充值""用户额度不足"），会原样透传给客户，客户以为是自己没钱，同时暴露上游是中转站。
2. 上游中转站没给某个模型配价格时的报错（`has not been priced by the administrator` / `尚未由管理员配置`）。这种渠道对该模型每次都会失败，应该换渠道。

### 为什么这样改

- 只补关键词，沿用现有处理：命中后替换为 529 `Overloaded`。529 在可重试范围内，会换渠道；全部失败时客户端收到 529 通用文案。按约定不做渠道冷却。
- 中文欠费词放进 `cKeywordsAlways`（400 和非 400 都生效）。这些词不会出现在正常的格式错误里，不存在 LT "400 用严格表"想避免的误伤问题。
- 同时把中文"上下文超长"措辞（`上下文长度`、`超出上下文`、`输入过长`）加进白名单 `gWhitelistKeywords`。白名单最先匹配，可以避免类似"输入过长，请充值升级套餐"这样的文案被误吞；上下文超长必须原样透传，Claude Code 才能触发自动压缩。

### 核心代码

`relay/channel/claude/claude_error_normalize.go`：

```go
var cKeywordsAlways = []string{
	...
	"operation not allowed",
	"余额",
	"欠费",
	"充值",
	"额度不足",
}

// Relays that list a model they cannot price will fail every request for it.
var unpricedModelKeywords = []string{
	"has not been priced by the administrator",
	"尚未由管理员配置",
}
```

### 测试

`claude_error_normalize_test.go` → `TestClassifyClaudeError_ChineseBalanceAndUnpricedModel`，包含方案里要求的两条反例：中文上下文超长原样透传；`insufficient tokens in prompt`（400）不被判为欠费。

---

## 2. 流中途报错按已收 usage 计费 — `5cd76a0dd`

### 处理的问题

上游在流的中途发来 `event: error` 时，`HandleStreamResponseData` 返回错误，`ClaudeHelper` 在结算之前返回，重试耗尽后 `RefundFailedRequestBilling` 退还全部预扣。但上游已经生成并收了这部分输出的钱，网关这边一分没收。再加上没有第 1 项的守卫，还会换渠道把第二个渠道的回复拼进来。

### 为什么这样改

分两种情况：

- **还没向客户端输出任何内容**：这时换渠道是安全的，错误照常返回给重试循环，最终失败时退款。和改之前一样。
- **已经输出过内容**：不能再重试（第 1 项），上游也已经计费，所以改为把错误事件发给客户端结束流，然后返回 usage、**不返回错误**。这样 `ClaudeHelper` 会走正常的 `PostTextConsumeQuota` 结算，而不是退款。

usage 的补全复用原有逻辑：把 `HandleStreamFinalResponse` 里补全 usage 的部分抽成 `finalizeClaudeStreamUsage`。`message_start` 里拿到的 input/cache 用真实值，输出用"文本估算 + 每个 tool_use 40 token"补齐。没有改价格、倍率或 quota 换算，结算仍走现有路径，符合 `.agents/rules/billing.md` 的要求。

### 核心代码

`relay/channel/claude/relay-claude.go` → `ClaudeStreamHandler`：

```go
if err == nil {
	HandleStreamFinalResponse(c, info, claudeInfo)
	...
	return claudeInfo.Usage, nil
}
// Before any output reached the client the request can still move to
// another channel, so the error goes to the retry loop.
if !common.ResponseBodyStarted(c) {
	return nil, err
}
// The upstream bills the partial output, so settle on the usage received
// so far plus estimates and end the stream with the error event.
if shouldApplyClaudeNormalize(info) {
	err = NormalizeClaudeError(err, err.StatusCode)
}
err.SetMessage(common.MessageWithRequestId(err.Error(), c.GetString(common.RequestIdKey)))
finalizeClaudeStreamUsage(c, info, claudeInfo)
if info.RelayFormat == types.RelayFormatClaude {
	_ = helper.ClaudeData(c, dto.ClaudeResponse{Type: "error", Error: err.ToClaudeError()})
} else {
	_ = helper.ObjectData(c, gin.H{"error": err.ToOpenAIError()})
	if info.RelayFormat == types.RelayFormatOpenAI {
		helper.Done(c)
	}
}
return claudeInfo.Usage, nil
```

开启了 ForceClaudeFormat/归一化的渠道，发给客户端的错误同样先经过 `NormalizeClaudeError`，不会泄露上游原文。

### 测试

`relay_claude_test.go` → `TestClaudeStreamHandlerMidStreamError`：
- 首帧就是 error：返回错误（可重试），客户端什么都没收到。
- 输出文字后出现 error：不返回错误；input 来自 `message_start`（1000），output 为正数估算值；客户端只收到一个 `event: error`，而且它是最后一个事件。

---

## 4. Claude 流被截断时补发终止帧 — `239efc9f7`

### 处理的问题

上游连接在没有发出 `message_stop` 的情况下结束（EOF 或读取出错），Claude 格式的收尾分支是空的，客户端拿到一个没有结束事件的半截流。Claude Code 会把它当成异常，丢弃或报错。

### 为什么这样改

- 只在以下条件**全部满足**时补帧：
  - 结束原因是 `eof` 或 `scanner_error`。按约定，**空闲超时不补**；客户端已断开的也不补，因为没人接收。
  - 没有收到过 `message_stop`。
  - 已经向客户端输出过内容（一个字都没输出就不补）。
- 补帧顺序遵循 Anthropic 流协议：如果还有没关闭的内容块，先补 `content_block_stop`；如果还没发过 `message_delta`，补一个 `stop_reason: end_turn`、`output_tokens` 为估算值的 `message_delta`；最后补 `message_stop`。
- 用流处理回调里已有的 `gjson` 跟踪当前打开的块序号，不需要改 relaykit 里的结构体。
- 开了归一化的渠道，补出来的 `message_delta` 也经过 `normalizeClaudeStreamEvent`，格式和官方一致。
- 计费不变：沿用原有"未完成就估算"的逻辑。

### 核心代码

`relay/channel/claude/relay-claude.go`：

```go
func closeTruncatedClaudeStream(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, openBlock int) {
	if info.StreamStatus.ResponseOutcome() == string(relaycommon.ResponseOutcomeCompleted) {
		return
	}
	endReason := info.StreamStatus.EndReason
	if endReason != relaycommon.StreamEndReasonEOF && endReason != relaycommon.StreamEndReasonScannerErr {
		return
	}
	if !common.ResponseBodyStarted(c) {
		return
	}
	...
	if openBlock >= 0 {
		helper.ClaudeChunkData(c, dto.ClaudeResponse{Type: "content_block_stop"}, fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, openBlock))
	}
	if !claudeInfo.Done {
		delta := fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":%d}}`, claudeInfo.Usage.CompletionTokens)
		if shouldNormalizeBody(info) {
			delta = normalizeClaudeStreamEvent(delta, "message_delta")
		}
		helper.ClaudeChunkData(c, dto.ClaudeResponse{Type: "message_delta"}, delta)
	}
	helper.ClaudeChunkData(c, dto.ClaudeResponse{Type: "message_stop"}, `{"type":"message_stop"}`)
}
```

### 测试

`relay_claude_test.go` → `TestClaudeStreamHandlerClosesTruncatedStream`：在内容块中途截断（补 3 种帧）、在 `message_delta` 之后截断（只补 `message_stop`）、完整的流（不补）、没有任何输出（不补）。每种情况都断言 3 种事件各只出现一次，且 `message_stop` 是最后一个事件。

---

## 5. 客户端断开后再读上游拿 usage — `5866009ca`

### 处理的问题

客户端断开时，流处理会立即关闭上游连接。但上游一旦开始处理请求就会计费，而真实的输入用量要等第一个事件（Claude 的 `message_start`）才拿得到。如果客户端在第一个事件之前断开，网关只能按估算的 input 计费，甚至记成 0，而上游照样收费。

### 为什么这样改

- 只在**还没处理过任何事件**时等待。已经处理过事件的（`message_start` 已经解析），说明 usage 已经拿到，照旧立即关闭上游，避免为没人接收的请求继续消耗上游 token。这样现有测试 `TestStreamScannerHandler_ClientCancelAbortsUpstreamAndReturns`（在第一个事件之后断开）的语义保持不变。
- 最多等 3 秒，拿到第一个事件或上游自己结束就提前退出。
- 等待期间第一个事件照常进入回调，usage 会被解析；写给客户端的数据会被丢弃，因为写函数在请求上下文结束后直接返回。
- 不把上游请求改成跟随客户端一起取消（不改 `NewRequestWithContext`）。上游已经开始处理并计费，提前断开反而拿不到 usage。

### 核心代码

`relay/helper/stream_scanner.go`：

```go
// clientGoneUsageGrace bounds how long a stream keeps reading the upstream after
// the client disconnects, waiting for the first event that carries usage.
var clientGoneUsageGrace = 3 * time.Second

...
case <-c.Request.Context().Done():
	info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, c.Request.Context().Err())
	select {
	case <-firstDataHandled:   // 已处理过第一个事件（或刚刚处理完）
	case <-stopChan:            // 上游已结束
	case <-time.After(clientGoneUsageGrace):
	}
}
cleanup()
```

`firstDataHandled` 在数据处理协程处理完第一个事件后关闭。

### 测试

`stream_scanner_test.go` → `TestStreamScannerHandler_ClientCancelBeforeFirstEventReadsUsage`（测试中把等待时间调成 200ms）：
- 断开后上游在等待期内发来第一个事件：事件被处理，客户端没有收到任何数据，之后上游连接被关闭。
- 上游一直不发数据：等待结束后返回，上游连接被关闭。

用 `-race -count=3` 跑过这两个用例，没有数据竞争。

---

## 10. 首字节超时 + 总预算 — `353b47db3`

### 处理的问题

dev 原来只有 `RELAY_RESPONSE_HEADER_TIMEOUT`（默认 1800 秒）限制等待响应头的时间。上游卡住时要等满 30 分钟。而且有的上游很快返回响应头，然后长时间不发第一个事件，这种情况响应头超时管不到。

### 为什么这样改

照 LT 的设计（文档 §1）：单次尝试有上限，所有重试共用一份总预算。如果没有总预算，N 次超时会串成 N×10 分钟，远远超过外层 nginx 的超时；外层先断开的话，网关自己的错误处理和计费逻辑都会被绕过。

- **单次上限** `RELAY_FIRST_BYTE_TIMEOUT`，默认 600 秒（10 分钟）。
- **总预算** `RELAY_FIRST_BYTE_BUDGET`，默认 1200 秒（20 分钟），从该请求的第一次上游尝试开始计时，起点存在 gin 上下文里。每次尝试实际可等的时间 = min(单次上限, 剩余预算)。任一值设为 0 表示关闭对应限制。
- **计时到"第一个响应体字节"**：拿到响应头后用 `bufio.Reader.Peek(1)` 等第一个字节，计时器仍在运行。读到的字节保留在 reader 里，后续处理拿到的是完整的响应体。
- **超时后返回 529 `Overloaded`**：529 在可重试范围内，会换渠道；Claude 格式下第 3 项会把它映射为 `overloaded_error`。按约定全部失败时统一返回 529。
- **预算用完时**：不再发起新的上游请求，直接返回带"不重试"标记的 529，作为最终结果。
- 放在 `doRequest` 里：这是所有渠道发请求的公共入口，所有格式和渠道都生效。首字节超时发生时还没向客户端输出任何内容，和第 1 项不冲突，可以安全重试。

### 核心代码

`relay/channel/api_request.go` → `doRequest`：

```go
firstByteTimeout := time.Duration(common2.RelayFirstByteTimeout) * time.Second
if firstByteTimeout > 0 {
	budgetStart, ok := common2.GetContextKeyType[time.Time](c, appconstant.ContextKeyFirstByteBudgetStart)
	if !ok {
		budgetStart = time.Now()
		common2.SetContextKey(c, appconstant.ContextKeyFirstByteBudgetStart, budgetStart)
	}
	if budget := time.Duration(common2.RelayFirstByteBudget) * time.Second; budget > 0 {
		remaining := budget - time.Since(budgetStart)
		if remaining <= 0 {
			return nil, upstreamOverloadedError(types.ErrOptionWithSkipRetry())
		}
		firstByteTimeout = min(firstByteTimeout, remaining)
	}
}
...
timer := time.AfterFunc(firstByteTimeout, func() {
	firstByteTimedOut.Store(true)
	cancel()
})
...
resp, err := relayClient.Do(req)
if err != nil && firstByteTimedOut.Load() {
	return nil, upstreamOverloadedError()
}
...
body := bufio.NewReader(resp.Body)
_, _ = body.Peek(1)
stopFirstByteTimer()
if firstByteTimedOut.Load() {
	_ = resp.Body.Close()
	return nil, upstreamOverloadedError()
}
resp.Body = &firstByteBody{Reader: body, body: resp.Body, cancel: cancelRequest}
```

`firstByteBody` 在 `Close` 时释放请求上下文，避免泄漏。

### 测试

`relay/channel/api_request_test.go` → `TestDoRequestFirstByteTimeout`（单次上限调成 1 秒）：
- 正常响应：响应体完整，被预读的第一个字节没有丢失。
- 一直不返回响应头：1 秒后返回 529，可重试，Claude 类型为 `overloaded_error`。
- 返回了响应头但没有响应体：同上。
- 剩余预算只有 1 秒（单次上限设为 600 秒）：1 秒后超时，说明剩余预算限制了单次时长。
- 预算已用完：不发请求，直接返回不重试的 529。

---

## 8. 非流式空 200 换渠道重试 — `9feb3530f`

### 处理的问题

上游非流式返回 200，但 `content` 为空、输出为 0，原来会原样透传，客户端收到一个空回复，网关按 0 输出计费，上游却收了 input 的钱。

### 为什么这样改

- 同时满足以下条件才视为上游故障：`content` 为空、输出 token 为 0（或没有 usage）、`stop_reason` **不是** `refusal`。
- **拒答必须豁免**：真实的拒答也是 `content: [] + stop_reason: refusal`，形状完全一样。LT 曾经把真拒答吞成假的 503，而上游照样收了 input 的钱。
- 判断放在写响应之前，返回 529 `Overloaded`。这时客户端什么都没收到，可以换渠道；全部失败时退款，客户端收到 529。
- 流式的空流不在本项范围内。

### 核心代码

`relay/channel/claude/relay-claude.go` → `HandleClaudeResponseData`：

```go
// An empty 200 with no output is an upstream fault: retry on another channel.
// A refusal has the same shape but is a real answer and is billed normally.
if len(claudeResponse.Content) == 0 && claudeResponse.StopReason != "refusal" &&
	(claudeResponse.Usage == nil || claudeResponse.Usage.OutputTokens == 0) {
	logger.LogWarn(c, ...)
	return types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, 529)
}
```

### 测试

`relay_claude_test.go` → `TestClaudeHandlerEmptyResponse`：空 `end_turn`（重试）、空且无 usage（重试）、空 `refusal`（正常计费）、正常回复（正常计费）。重试的情况都断言客户端没有收到任何数据、错误可重试。

---

## 验证

执行过的命令和结果：

```bash
go build ./...                                              # 通过
cd relaykit && GOWORK=off go build ./... && GOWORK=off go test ./...   # 通过
go test ./service/ ./controller/ ./relay/... ./setting/... ./middleware/ ./common/   # 全部通过
go test -race -count=1 ./relay/channel/claude/              # 通过
go test -race -count=1 ./relay/channel/ -run FirstByte      # 通过
go test -race -count=3 ./relay/helper/ -run ClientCancel    # 通过
cd relaykit && GOWORK=off go test -race ./types/            # 通过
```

**已知问题（不是这次改动引入的）**：`go test -race ./service/` 和 `go test -race ./relay/helper/`（全量）会报数据竞争，位置在 `logger/logger.go:114`（`logCount++` 没有加锁，代码注释写明是有意为之），以及 `service/error_test.go` 里使用 `t.Parallel()` 的测试。在合并后、改动前的提交 `539f711fc` 上跑同样的 `relay/helper` race 测试，同样报竞争，所以和本次改动无关。

## 遗留事项

1. **AWS Bedrock 渠道**：第 2 项（中途报错按已收计费）和第 4 项（补终止帧）只改了 Anthropic 渠道的 `ClaudeStreamHandler`。Bedrock 的流处理在 `relay/channel/aws/relay-aws.go`，有自己的循环，这次没有改。
2. **第 10 项的连接关闭**：编写测试时发现，首字节超时取消请求后，测试服务端的处理函数没有立刻感知到连接断开（测试里改用释放信号来收尾）。也就是说，超时后上游那边的请求不一定会被立即中止，上游可能还会继续生成一段时间。建议上线后观察，必要时在超时时强制关闭底层连接。
3. 第 7、9、12、13 项按决定未做。保活 ping 目前仍然默认关闭；第 1 项已经保证以后开启 ping 不会影响重试。
