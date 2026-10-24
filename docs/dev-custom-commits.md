# dev 分支自定义提交说明（Wxrjiayou / root）

本文档整理 `dev` 分支上由 **Wxrjiayou** 与 **root** 提交的全部改动（共 15 个提交，2026-08-02 ~ 2026-09-28），说明每项功能的目的、实现方式、核心代码，以及仓库内是否存在对应单元测试。

> 用途：同步上游 tag 时，作为冲突解决与功能回归的对照清单。

## 提交总览

| # | 提交 | 作者 | 功能 | 单元测试 |
|---|------|------|------|----------|
| 1 | `4aef21b2e` | Wxrjiayou | 计费表达式新增 `maxEdge()` / `pixels()` | ❌ 无 |
| 2 | `0767e92dc` | Wxrjiayou | 504/524 允许自动重试 | ⚠️ 已有测试未同步，**当前失败** |
| 3 | `20350572d` | root | 重试按优先级循环（取模） | ⚠️ 有测试，但只验证取模算式，未调用业务函数 |
| 4 | `2fa1ef9ae` + `251bc6869` | root | OpenAI 流首包即错误 → 返回 502 触发重试 | ✅ 有 |
| 5 | `34fa3a200` | root | 上游 429/503 错误体统一改写 | ✅ 有 |
| 6 | `8b15a84a9` | root | Force Claude Format：Max 响应伪装为官方格式（主开关 + 头 + 限流头） | ⚠️ 部分 |
| 7 | `565aaf39f` | root | 响应体字段归一化（container / inference_geo 等） | ✅ 有 |
| 8 | `06c01e6a2` | root | Claude 错误分类归一化 + 隐藏分组/渠道信息 | ⚠️ 部分 |
| 9 | `2115f14eb` | root | 拆分 Body / Header 两个独立开关 | ⚠️ 部分（仅后端开关判定） |
| 10 | `59bc977f5` | root | 删除 `input_transformations` / `diagnostics` / `usage.cache_creation` | ✅ 有 |
| 11 | `94075529c` | root | 头部格式对齐官方（request-id / org-id / workspace-id） | ✅ 有 |
| 12 | `c57a08204` | root | 移除流式响应的合成头注入 | ❌ 无 |
| 13 | `13955fb34` | root | P0 错误归一化修复（400 不重试、反序列化错误脱敏、关键词补充、OpenAI 路径接入） | ⚠️ 部分 |
| 14 | `2d5dd817c` | root | 删除 `stop_details` / `estimated_tokens`，清理签名 protobuf 字段 | ✅ 有 |

测试执行结果（2026-10-24，`go test`）：

```
ok   relay/channel/claude
ok   service
ok   model
ok   relay/channel/openai
ok   pkg/billingexpr
ok   relay/helper
FAIL setting/operation_setting   ← TestShouldRetryByStatusCode 等 3 个用例
```

---

## 1. 计费表达式 `maxEdge()` / `pixels()` — `4aef21b2e`

**目的**：按图片分辨率计费时，不用在表达式里穷举所有 `size`。

- `maxEdge(param("size"))`：`"3840x2160"` → `3840`
- `pixels(param("size"))`：`"3840x2160"` → `8294400`
- 示例：`maxEdge(param("size")) > 2048 ? 130000.0 : 80000.0`

**实现**：在编译期环境中注册函数签名（`pkg/billingexpr/compile.go`），在运行期环境中注册实际实现（`pkg/billingexpr/run.go`）。

```go
// pkg/billingexpr/compile.go — 编译期占位，保证表达式能通过类型检查
"maxEdge": func(interface{}) float64 { return 0 },
"pixels":  func(interface{}) float64 { return 0 },

// pkg/billingexpr/run.go — 运行期实现
"maxEdge": func(source interface{}) float64 {
    w, h := parseDimensions(source)
    if w == 0 && h == 0 {
        return 0
    }
    if w > h {
        return float64(w)
    }
    return float64(h)
},
"pixels": func(source interface{}) float64 {
    w, h := parseDimensions(source)
    return float64(w) * float64(h)
},

func parseDimensions(source interface{}) (int, int) {
    s, ok := source.(string)
    if !ok { return 0, 0 }
    parts := strings.SplitN(strings.ToLower(strings.TrimSpace(s)), "x", 2)
    // 宽、高必须都能解析成正整数，否则返回 0,0
    ...
}
```

**单元测试**：❌ 无。`pkg/billingexpr/billingexpr_test.go` 中没有用例覆盖 `maxEdge` / `pixels` / `parseDimensions`。
建议补一个表驱动测试，覆盖 `"1024x1024"`、`"3840X2160"`、`" 800 x 600 "`、`"auto"`、`"0x100"`、`nil`、非字符串输入，以及一个完整的表达式求值用例。

---

## 2. 504/524 允许自动重试 — `0767e92dc`

**目的**：网关超时（504、Cloudflare 524）也走普通重试逻辑，不再无条件跳过。

**实现**：`setting/operation_setting/status_code_ranges.go`

```go
var AutomaticRetryStatusCodeRanges = []StatusCodeRange{
    {Start: 100, End: 199},
    {Start: 300, End: 399},
    {Start: 401, End: 407},
    {Start: 409, End: 499},
    {Start: 500, End: 599}, // 原来是 500-503、505-523、525-599
}

var alwaysSkipRetryStatusCodes = map[int]struct{}{} // 原来是 {504, 524}
```

**单元测试**：⚠️ `setting/operation_setting/status_code_ranges_test.go` 是上游原有测试，仍然断言旧行为，因此**当前有 3 个用例失败**：

| 用例 | 失败断言 |
|------|----------|
| `TestShouldRetryByStatusCode` | `require.False(ShouldRetryByStatusCode(504))`（第 65 行） |
| `TestShouldRetryByStatusCode_DefaultMatchesLegacyBehavior` | 同上（第 78 行） |
| `TestIsAlwaysSkipRetryStatusCode` | `require.True(IsAlwaysSkipRetryStatusCode(504))`（第 84 行） |

需要把这些断言改成新行为（504/524 应该重试、不在 always-skip 列表中）。另外，上游后续的提交 `50ffa639a`（"guard new 504/524 status remaps with risk confirmation"）也依赖 504/524 的特殊处理，同步上游时这里必然冲突。

---

## 3. 重试时按优先级循环 — `20350572d`

**目的**：重试次数超过优先级档位数后，回到最高优先级重新开始，而不是一直停留在最低档位。

**实现**：`model/ability.go`（数据库路径）和 `model/channel_cache.go`（内存缓存路径）都把截断改成了取模。

```go
// model/ability.go  getPriority
retry = retry % len(priorities)
priorityToUse := priorities[retry]

// model/channel_cache.go  GetRandomSatisfiedChannel
retry = retry % len(uniquePriorities)
targetPriority := int64(sortedUniquePriorities[retry])
```

**单元测试**：⚠️ `model/channel_priority_cycle_test.go`（`TestPriorityCycleModulo` / `SingleTier` / `TwoTiers`）可以通过，但测试只在本地切片上重新实现了 `idx := retry % len(priorities)`，**没有调用 `getPriority` 或 `GetRandomSatisfiedChannel`**。即使业务代码回退，这些测试也照样能通过，所以实际上不提供回归保护。建议改为构造多档优先级的渠道，直接调用 `GetRandomSatisfiedChannel(group, model, retry, ...)` 来断言选中的档位。

---

## 4. OpenAI 流首包即错误 → 返回 502 并重试 — `2fa1ef9ae`（测试在 `251bc6869`）

**目的**：有些上游返回 HTTP 200 + SSE，但第一条数据就是 `{"error":...}`。原逻辑会把它当作成功的空响应透传。现在的处理是：如果还没有向客户端发送过任何数据，就返回 502，交给重试逻辑换渠道（502 落在可重试区间 500-599 内）。

**实现**：`relay/channel/openai/relay-openai.go` → `OaiStreamHandler`

```go
if info.SendResponseCount == 0 && lastStreamData != "" {
    var probe struct {
        Error *struct {
            Message string `json:"message"`
            Code    string `json:"code"`
        } `json:"error"`
    }
    if err := common.UnmarshalJsonStr(lastStreamData, &probe); err == nil && probe.Error != nil {
        errMsg := fmt.Sprintf("upstream stream error: [%s] %s", probe.Error.Code, probe.Error.Message)
        logger.LogError(c, errMsg)
        return nil, types.NewOpenAIError(fmt.Errorf("%s", errMsg), types.ErrorCodeBadResponse, http.StatusBadGateway)
    }
}
```

**单元测试**：✅ `relay/channel/openai/relay_stream_error_test.go`
- `TestOaiStreamHandler_UpstreamErrorInSSE_ReturnsError`：首包为错误 → 返回 502
- `TestOaiStreamHandler_NormalStream_NoError`：正常流不受影响
- `TestOaiStreamHandler_ErrorAfterValidChunks_NoRetry`：已经发送过数据后再出现错误 → 不返回错误（不能重试）

---

## 5. 上游 429/503 错误体统一改写 — `34fa3a200`

**目的**：不向客户端暴露上游的限流/不可用原文（可能泄露上游身份）。状态码保持不变，原文写入 warn 日志。后来在 `06c01e6a2` 中，提示文案改成了 `"Overloaded"`。

**实现**：`service/error.go` → `RelayErrorHandler` 用 `defer` 统一覆盖返回值；任务接口在 `controller/relay.go` → `respondTaskError` 中做同样处理。

```go
const upstreamUnavailableMessage = "Overloaded"

func RelayErrorHandler(ctx context.Context, resp *http.Response, showBodyWhenFail bool) (newApiErr *types.NewAPIError) {
    var originalBodyPreview string
    defer func() {
        if newApiErr != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
            if originalBodyPreview != "" {
                logger.LogWarn(ctx, fmt.Sprintf("upstream %d intercepted, original: %s", resp.StatusCode, originalBodyPreview))
            }
            newApiErr = types.NewOpenAIError(errors.New(upstreamUnavailableMessage), types.ErrorCodeBadResponseStatusCode, resp.StatusCode)
        }
    }()
    ...
    originalBodyPreview = responseBodyPreview
```

**单元测试**：✅ `service/error_test.go`
- `TestRelayErrorHandlerInterceptsUpstream429And503`
- `TestRelayErrorHandlerDoesNotInterceptOtherStatusCodes`
- `TestRelayErrorHandlerInterceptionLogsOriginalBody`
- `TestRelayErrorHandlerInterceptionPreservesShowBodyWhenFailBehavior`

`respondTaskError`（任务接口）的改写没有测试覆盖。

---

## 6–14. Force Claude Format（Claude Max 渠道伪装为官方 API）

这一组提交（6–14）是同一个功能的连续迭代，下面按模块说明。

### 6.1 渠道开关 — `8b15a84a9`、`2115f14eb`

`relaykit/dto/channel_settings.go` 中的 `ChannelSettings` 新增了三个字段：

```go
ForceClaudeFormat      bool `json:"force_claude_format,omitempty"`      // 主开关，等价于同时开启下面两个
NormalizeClaudeBody    bool `json:"normalize_claude_body,omitempty"`    // 响应体归一化
NormalizeClaudeHeaders bool `json:"normalize_claude_headers,omitempty"` // 响应头归一化
```

判定函数在 `relay/channel/claude/claude_normalize.go`：

```go
func shouldNormalizeBody(info *relaycommon.RelayInfo) bool {
    if info == nil || info.ChannelMeta == nil {
        return false
    }
    return info.ChannelSetting.NormalizeClaudeBody || info.ChannelSetting.ForceClaudeFormat
}
// shouldNormalizeHeaders 同理；shouldApplyClaudeNormalize = body || headers
```

前端改动：`web/src/features/channels/components/drawers/channel-mutate-drawer.tsx` 增加了 3 个 Switch（主开关打开时，两个子开关自动勾选并禁用）；`channel-form.ts` 增加了对应的表单字段；`en.json` / `zh.json` 增加了文案。

**单元测试**：✅ `TestGateFunctions`、`TestGateFunctions_NilInfo`、`TestGateFunctions_NilChannelMeta`。前端改动没有测试。

### 6.2 响应体归一化 — `565aaf39f`、`59bc977f5`、`2d5dd817c`

删除 Max 特有字段，补齐官方特有字段。

| 处理 | 非流式 | 流式事件 |
|------|--------|----------|
| 删除 `usage.iterations` | ✔ | `message_delta` |
| 删除 `usage.cache_creation` | ✔ | `message_start` / `message_delta` |
| 删除 `context_management` | ✔ | `message_start` / `message_delta` |
| 删除 `input_transformations`、`diagnostics` | ✔ | `message_start` |
| 删除 `stop_details` | ✔ | `message_start` / `message_delta` |
| 删除 `delta.estimated_tokens` | — | `content_block_delta` |
| `usage.inference_geo` 已存在时改为 `"global"` | ✔ | `message_start` |
| `container` 不存在时补 `null` | ✔ | `message_start` / `message_delta` |
| thinking 签名去掉 protobuf 字段 14/17/22 | ✔ | `signature_delta` |

核心代码（使用 `gjson`/`sjson` 直接改原始 JSON，不经过结构体反序列化，因此不会丢失未知字段）：

```go
func normalizeClaudeBody(data []byte) []byte {
    data = deleteBytes(data, "usage.iterations")
    data = deleteBytes(data, "usage.cache_creation")
    data = deleteBytes(data, "context_management")
    data = deleteBytes(data, "input_transformations")
    data = deleteBytes(data, "diagnostics")
    data = deleteBytes(data, "stop_details")
    if gjson.GetBytes(data, "usage.inference_geo").Exists() {
        data = setBytes(data, "usage.inference_geo", "global")
    }
    if !gjson.GetBytes(data, "container").Exists() {
        data = setBytes(data, "container", nil)
    }
    gjson.GetBytes(data, "content").ForEach(func(key, value gjson.Result) bool {
        if value.Get("type").String() == "thinking" && value.Get("signature").String() != "" {
            sig := value.Get("signature").String()
            if cleaned := stripMaxSignatureFields(sig); cleaned != sig {
                data = setBytes(data, "content."+key.String()+".signature", cleaned)
            }
        }
        return true
    })
    return data
}
```

签名清理：先 base64 解码，再按 protobuf wire format 逐层解析（顶层字段 2 → 内层字段 1 → 删除字段 14/17/22），然后重新编码。任何解析异常都会原样返回签名，保证不会破坏签名：

```go
var stripSignatureFieldTags = map[uint64]bool{14: true, 17: true, 22: true}

func stripMaxSignatureFields(sig string) string {
    raw, err := base64.StdEncoding.DecodeString(sig)
    if err != nil {
        return sig
    }
    out, changed := stripProtoFieldsFromSignature(raw) // → rebuildInnerField2 → stripFieldsFromMessage
    if !changed {
        return sig
    }
    return base64.StdEncoding.EncodeToString(out)
}
```

接入点在 `relay/channel/claude/relay-claude.go`：

```go
// 流式：HandleStreamResponseData
if shouldNormalizeBody(info) {
    data = normalizeClaudeStreamEvent(data, claudeResponse.Type)
}

// 非流式：HandleClaudeResponseData（仅 RelayFormatClaude，即 /v1/messages）
case types.RelayFormatClaude:
    if shouldNormalizeBody(info) {
        responseData = normalizeClaudeBody(data)
    } else {
        responseData = data
    }
```

**单元测试**：✅ `relay/channel/claude/claude_normalize_test.go`
- 非流式：`TestNormalizeClaudeBody_Full` / `_AlreadyOfficial` / `_NoIterationsNoContextMgmt` / `_ThinkingSignatureStripped`
- 流式：`TestNormalizeClaudeStreamEvent_MessageStart` / `_MessageStartWithContextMgmt` / `_MessageDelta` / `_MessageDeltaClean` / `_ContentBlockDelta` / `_ContentBlockDeltaThinking` / `_SignatureDeltaStripped` / `_Ping`
- 签名：`TestStripMaxSignatureFields`

缺口：没有测试验证 `HandleStreamResponseData` / `HandleClaudeResponseData` 中开关打开/关闭时的接入行为；签名解析的异常输入（截断数据、非法 varint）也没有用例。

### 6.3 响应头归一化 — `8b15a84a9`、`94075529c`、`c57a08204`

仅对**非流式** `/v1/messages` 生效。流式响应的头注入在 `c57a08204` 中已经移除。

非流式流程（`writeClaudeNormalizedResponse`）：
1. 上游响应头只透传白名单：`content-type`、`cache-control`、`anthropic-version`、`x-should-retry`、`retry-after`。
2. `request-id` / `anthropic-organization-id` / `anthropic-workspace-id`：上游有就用上游的值，没有就合成。
3. 补上官方的安全头：`content-security-policy`、`strict-transport-security`、`x-robots-tag`。
4. 生成 `anthropic-ratelimit-*` 限流头。

```go
func genRequestID() string {               // req_01 + 7 位时间戳 base62 + 15 位随机，类 ULID
    ts := uint64(time.Now().UnixMilli())
    return "req_01" + encodeUint64Base62(ts, 7) + randBase62(15)
}

func deriveOrgID(tokenKey string) string {  // 按令牌稳定派生，格式为 UUID v4
    h := sha256.Sum256([]byte("org:" + tokenKey))
    h[6] = (h[6] & 0x0F) | 0x40
    h[8] = (h[8] & 0x3F) | 0x80
    return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func deriveWorkspaceID(tokenKey string) string {
    h := sha256.Sum256([]byte("wrkspc:" + tokenKey))
    return "wrkspc_01" + encodeBytesBase62(h[:16], 22)
}
```

在流式路径中，`relay/helper/stream_scanner.go` 会在开关打开时跳过 `copyCodexSSEHeaders`，避免上游头泄露：

```go
if info.ChannelMeta == nil || !(info.ChannelSetting.NormalizeClaudeHeaders || info.ChannelSetting.ForceClaudeFormat) {
    copyCodexSSEHeaders(c, resp)
}
```

限流头（`relay/channel/claude/claude_ratelimit.go`）：按 `TokenId` 维护每分钟的请求数和 token 计数窗口（`sync.Map` + 原子计数，后台协程每 2 分钟清理一次过期窗口）。请求结束时调用 `recordClaudeUsage` 累加，`setClaudeRatelimitHeaders` 用固定上限（RPM 1000 / TPM 400000 / 输入 320000 / 输出 80000）减去已用量，得到 remaining。

**单元测试**：⚠️ 部分覆盖
- ✅ `TestGenRequestID`、`TestDeriveOrgID_Stable`、`TestDeriveOrgID_UUIDv4Format`、`TestDeriveWorkspaceID_Format`、`TestDeriveWorkspaceID_DiffersFromOrgID`
- ❌ `writeClaudeNormalizedResponse`（白名单过滤、上游头优先、Content-Length、状态码）没有测试
- ❌ `claude_ratelimit.go`（`recordClaudeUsage` / `setClaudeRatelimitHeaders` / 分钟窗口切换 / remaining 不为负）没有测试
- ❌ `stream_scanner.go` 中跳过 `copyCodexSSEHeaders` 的行为没有测试
- ❌ `IsStreamHeaderAllowed`（导出函数，目前仓库中没有调用方）没有测试

### 6.4 错误归一化 — `06c01e6a2`、`13955fb34`

**目的**：对于暴露订阅、代理、计费信息的上游错误，统一替换成官方风格的 `Overloaded` / `Internal server error`；属于客户端自身问题的错误（上下文超长等）保持原样透传。

分类顺序（`relay/channel/claude/claude_error_normalize.go` → `classifyClaudeError`）：

| 阶段 | 判定 | 结果 |
|------|------|------|
| G | 白名单：`prompt is too long`、`max_tokens`、`invalid signature` 等 | 原样透传 |
| A/B | 订阅、额度、OAuth、Claude Code 等关键词，以及正则 `(reached\|hit) your … limit` | 529 `overloaded_error` |
| C | 状态码 402、`credit`/`quota` 类关键词；非 400 时的 `insufficient`/`billing` | 529 |
| D-2 | `refusal guard` 等本地拦截 | 529 |
| Bedrock | 不可用上游的特征串（如 `555420`） | 529 |
| E | 仅 400：`upstream rate limit exceeded` 等 | 429 `overloaded_error` |
| F-2 | 响应体是 HTML | `api_error` "Internal server error"（保留原状态码） |
| 其他 | — | 原样透传 |

```go
func NormalizeClaudeError(apiErr *types.NewAPIError, upstreamStatusCode int) *types.NewAPIError {
    if apiErr == nil {
        return apiErr
    }
    switch classifyClaudeError(apiErr.Error(), upstreamStatusCode) {
    case actionReplaceOverloaded:
        return types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, 529)
    case actionReplaceOverloaded429:
        return types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, 429)
    case actionReplaceInternalError:
        return types.WithClaudeError(types.ClaudeError{Type: "api_error", Message: "Internal server error"}, apiErr.StatusCode)
    }
    return apiErr
}
```

接入点（`2115f14eb` 之后对所有 Claude 上游**无条件**生效，不受 ForceClaudeFormat 开关控制）：
- `relay/claude_handler.go` → `ClaudeHelper`：上游返回非 200 时调用
- `relay/compatible_handler.go` → `TextHelper`、`relay/responses_handler.go`、`relay/chat_completions_via_responses.go`：在 `claude.IsClaudeUpstream(info.ApiType)`（Anthropic 或 AWS Bedrock）时调用

同批次的其他改动：
- **隐藏分组和渠道信息**（`middleware/distributor.go`、`controller/relay.go` → `getChannel`）：选不到渠道时，客户端只会收到 `"The model is currently unavailable, please try again later"`，分组名、模型名和 `(distributor)`/`(retry)` 标记只写入 warn 日志。
- **请求校验错误改为 400 且不重试**（`controller/relay.go`）：
  ```go
  newAPIError = types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusBadRequest), types.ErrOptionWithSkipRetry())
  ```
- **反序列化错误脱敏**（`relay/helper/valid_request.go`）：所有 `GetAndValidate*Request` 都经过这个函数，避免把 Go 内部类型名返回给客户端：
  ```go
  func sanitizeUnmarshalError(err error) error {
      var ute *json.UnmarshalTypeError
      if errors.As(err, &ute) {
          if ute.Field != "" {
              return fmt.Errorf("invalid value for field %s", ute.Field)
          }
          return fmt.Errorf("invalid request body")
      }
      return err
  }
  ```
  注意：这里直接 import 了 `encoding/json`，但只用到 `*json.UnmarshalTypeError` 这个类型，没有调用 marshal/unmarshal，所以符合 AGENTS.md 的 JSON 规范。

**单元测试**：⚠️ 部分覆盖
- ✅ `relay/channel/claude/claude_error_normalize_test.go`：覆盖各分类阶段（`TestClassifyClaudeError_*`，包括 G 优先级、402、Max 第三方关键词、Bedrock）、替换结果（`TestNormalizeClaudeError_*`）以及 `TestIsClaudeUpstream`
- ❌ `sanitizeUnmarshalError` 没有测试（例如 `max_tokens: -1` → `invalid value for field max_tokens`）
- ❌ 请求校验错误返回 400 + SkipRetry 的行为没有测试
- ❌ distributor / getChannel 文案脱敏没有测试
- ❌ 各 handler（Claude / compatible / responses）接入 `NormalizeClaudeError` 的行为没有测试

---

## 测试缺口汇总（建议按优先级处理）

1. **修复失败的测试**：`setting/operation_setting/status_code_ranges_test.go` 中关于 504/524 的断言（提交 #2）。
2. **计费相关**：给 `maxEdge` / `pixels` / `parseDimensions` 补表驱动测试（提交 #1）。它们直接决定计费金额，按 AGENTS.md 的计费安全要求应该有测试。
3. **把优先级循环测试改为调用真实函数**（提交 #3）。
4. `sanitizeUnmarshalError`，以及校验错误返回 400 且不重试。
5. `writeClaudeNormalizedResponse` 的头白名单 / 上游优先逻辑，以及 `claude_ratelimit.go` 的窗口计数。
6. 签名 protobuf 解析的异常输入用例。

## 同步上游时的高冲突文件

`setting/operation_setting/status_code_ranges.go`、`model/ability.go`、`model/channel_cache.go`、`relay/channel/openai/relay-openai.go`、`relay/channel/claude/relay-claude.go`、`relay/helper/stream_scanner.go`、`relay/helper/valid_request.go`、`relay/claude_handler.go`、`relay/compatible_handler.go`、`relay/responses_handler.go`、`service/error.go`、`controller/relay.go`、`middleware/distributor.go`、`relaykit/dto/channel_settings.go`，以及前端的 `channel-mutate-drawer.tsx` / `channel-form.ts` / `i18n` 文件。
