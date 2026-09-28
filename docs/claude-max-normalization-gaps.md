# Claude Max 渠道归一化整改建议

> 对照参考中转站设计，审计 new-api ForceClaudeFormat 渠道当前行为与官方 Claude API 的差距。
> 仅记录结论和方向，不涉及具体代码实现。
>
> 审计日期：2026-09-24

---

## 术语约定

| 简称 | 含义 |
|---|---|
| 官方 API | Anthropic 公开 Messages API (`api.anthropic.com`) |
| Max 渠道 | 通过 Claude Max 订阅号/OAuth 凭据接入的上游 |
| 参考设计 | 另一中转站的拦截规则（本文对照基准） |
| FCF | ForceClaudeFormat，new-api 中让 Max 渠道行为更接近官方的开关 |

---

## 一、错误拦截关键词

### 1.1 上下文超长白名单（透传类） — 合格

当前 `gWhitelistKeywords` 已覆盖参考设计全部 7 条：`input is too long`、`too many tokens`、`token limit`、`context length`、`maximum context`、`exceeds the model`、`content length exceeds`。

额外还有 `prompt is too long`、`request too large`、`payload too large`、`max_tokens`、`invalid signature`、`does not support oneof/allof/anyof`、`tool_result block`、`image exceeds`。这些多出来的均属于客户端确定性错误，透传是正确行为。

参考设计特别提到 `request too large for this upstream` 应原样透传——当前白名单中 `request too large` 会匹配到它，行为一致。

**结论：无需改动。**

---

### 1.2 余额耗尽类 — 小幅调整

当前 `cKeywordsAlways` 和 `cKeywordsNon400` 已覆盖绝大部分。

**需补充**：

| 关键词 | 说明 |
|---|---|
| `余额` | 中文余额不足提示 |
| `欠费` | 中文欠费提示 |
| `充值` | 中文充值提示 |
| `额度不足` | 中文额度不足提示 |

中文关键词的必要性取决于上游是否会返回中文错误。如果确认上游只返回英文，可不加。

**需调整分档**：

| 关键词 | 当前 | 参考设计 | 建议 |
|---|---|---|---|
| `credit balance is too low` | 任意状态码拦 | 仅 400 拦 | 降为仅 400 |
| `purchase credits` | 任意状态码拦 | 仅 400 拦 | 降为仅 400 |

这两个词在非 400 场景下可能出现在正常上下文中（如模型回复本身），降级可减少误伤风险。实际风险不高，优先级低。

---

### 1.3 订阅限额类 — 合格

`abKeywords` + `abRegexes` 与参考设计完全一致，无需改动。

---

### 1.4 MAX / 订阅号 / 第三方特征 — 大量缺失（P0）

当前仅覆盖 7 条，参考设计共 19 条（含正式上线后可单独回退的冗余条目）。

**需补充 12 条**：

| 关键词 | 泄漏场景 |
|---|---|
| `can't use claude code` | 订阅号被封（account on hold） |
| `only authorized for use with claude code` | OAuth 凭据只认 Claude Code 客户端 |
| `stream ended without receiving any events` | CLI 包装型中转把 CLI 渲染的错误包成 400 |
| `previous_message_id` | MAX thread 状态错配（公开 API 无此参数） |
| `when thread is set` | 同上 |
| `organization has been disabled` | 组织被禁 |
| `organization has disabled` | 组织禁用了订阅/API 访问（变体） |
| `account has been disabled` | 账号被禁 |
| `account is on hold` | 账号冻结 |
| `oauth token` | OAuth 凭据错误（401 被中转压成 400 时兜底） |
| `oauth authentication` | OAuth 认证不支持 |
| `please run /login` | CLI 登录提示 |
| `claude code` | 泛匹配兜底 — 官方 API 错误正文从不提 "claude code" |
| `x-anthropic-billing-header` | Bedrock 拒绝 Claude Code 计费头 |

> `claude code` 作为泛匹配覆盖了 `can't use claude code` 和 `only authorized for use with claude code`，但参考设计建议保留长串以便单独回退。

**风险评估**：`claude code` 匹配范围较宽。参考设计称用 90 天下游 400 日志回放无误伤，但建议上线前用自身日志跑一遍确认。

---

### 1.5 400 内限流 — 合格

`eKeywords400` + `eRegexes400` 与参考设计一致，无需改动。

---

### 1.6 死上游签名 — 完全缺失（P1）

参考设计有 4 个 Bedrock / 死上游识别关键词，当前代码没有任何一个：

| 关键词 | 说明 |
|---|---|
| `deployment request could not be completed` | 上游 Bedrock 部署失败 |
| `independently manages customer access` | Bedrock 区域限制 |
| `555420` | Bedrock 错误码 |
| `access to bedrock models is not allowed` | Bedrock 权限被拒 |

这类错误意味着上游账号的 Bedrock 接入已失效，应换号并标记账号不可用。

---

### 1.7 `d2Keywords`（拦截守卫类）— 参考设计未提及

当前代码有 `stopped locally before forwarding` 和 `refusal guard`，被归类为 `actionReplaceOverloaded`（改写为 529）。参考设计没有提到这组。

**建议保留**。这些是 Max 特有的内容审核拦截行为，官方 API 不会返回这类错误，改写成 529 是合理的。

---

## 二、状态码处理

### 2.1 按状态码换号规则

| 状态码 | 参考设计行为 | 当前行为 | 差距 |
|---|---|---|---|
| 200 | 不匹配 | ✅ 不重试 | 无 |
| 400 | 不换号（除非命中关键词） | ✅ 不重试（不在 retry 范围） | 无 |
| 401 | 换号 | ✅ 在重试范围 401-407 | 无 |
| 402 | 当余额耗尽 | ✅ `classifyClaudeError` 对 402 返回 `actionReplaceOverloaded` | 无 |
| 403 | 换号 | ✅ 在重试范围 | 无 |
| 404 | 换号 | ✅ 在重试范围 409-499 | 无 |
| 429 | 换号 | ✅ 在重试范围 | 无 |
| 5xx | 换号 | ✅ 500-599 在重试范围（504/524 除外） | 无 |

**结论：状态码触发的换号/重试逻辑基本对齐。**

### 2.2 整池失败后的客户端响应

| 行为 | 参考设计 | 当前 | 差距 |
|---|---|---|---|
| 状态码 | 529 | ✅ 529（经 `NormalizeClaudeError` 改写） | 无 |
| 错误正文 | 通用 `overloaded_error`，不透传原文 | ✅ `{"type":"overloaded_error","message":"Overloaded"}` | 无 |
| `Retry-After` header | `Retry-After: 5` | ❌ 不设置 | **需补充** |

**建议**：在 `NormalizeClaudeError` 返回 529 时，或在 `controller/relay.go` 的错误响应 defer 中，对 529 响应追加 `Retry-After: 5` header。

### 2.3 模型不存在 → 503（应为 404）

当 distributor 找不到模型对应的渠道时，返回 503 `"The model is currently unavailable, please try again later"`。

这不是 Max 归一化的问题，而是 **new-api 通用分发层的设计**。503 表示"暂时不可用"，SDK 会重试；404 表示"确定不存在"，SDK 不重试。

**现状原因**：分发层无法区分"模型压根不存在"和"模型存在但所有渠道暂时不可用"，统一用 503。

**建议**：

- 最小改动方案：如果模型名在全局模型列表中根本不存在（而非仅当前 group 无渠道），返回 404。只有当模型存在但渠道全部下线/禁用时才 503。
- 不改方案：接受当前行为，因为对客户端来说"模型不可用"比"模型不存在"更安全（不暴露模型配置信息）。但需注意 SDK 重试浪费。

---

## 三、错误响应格式

### 3.1 distributor 层错误格式不感知请求格式（P1）

`middleware/distributor.go` 的所有错误响应都走 `abortWithOpenAiMessage`，返回 OpenAI 格式：

```json
{"error": {"message": "...", "type": "new_api_error", "code": ""}}
```

对 Claude 格式请求（`/v1/messages`），客户端 SDK 期望的是：

```json
{"type": "error", "error": {"type": "not_found_error", "message": "..."}}
```

当前 `controller/relay.go` 的 `Relay()` defer 已区分格式（Claude 用 `ToClaudeError()`，OpenAI 用 `ToOpenAIError()`），但 distributor 在 `Relay()` 之前就 abort 了，走不到 Relay 的格式分支。

**建议**：distributor 根据请求路径（如 `/v1/messages`）判断格式，对 Claude 格式请求使用 Claude 错误格式返回。

### 3.2 响应 header 白名单

FCF 开启时，非流式响应只透传白名单 header：

| 当前白名单 | 参考设计白名单 |
|---|---|
| `content-type` | — |
| `cache-control` | — |
| `anthropic-version` | — |
| `x-should-retry` | — |
| `retry-after` ✅ | `retry-after` ✅ |
| — | `x-request-id` ❌ 缺失 |

当前代码自己生成 `request-id`（非 `x-request-id`），不透传上游的 `x-request-id`。

**建议**：

- 参考设计只放行 `retry-after` 和 `x-request-id`，当前多放行了 `anthropic-version`、`x-should-retry`、`cache-control`。这些额外 header 不构成泄漏风险（都是官方 API 也会返回的），保留即可。
- 如需更严格对齐，可移除多余的白名单项，但优先级低。

---

## 四、error normalization 覆盖范围

### 4.1 OpenAI 格式请求走 Claude 渠道时不经过归一化（P0）

`NormalizeClaudeError` 仅在 `relay/claude_handler.go`（Claude 原生格式路径）中调用。

当用户用 OpenAI 格式（`/v1/chat/completions`）请求，后端映射到 Claude 渠道时，上游错误走 `relay/relay_handler.go` 的通用路径，**完全不经过 `NormalizeClaudeError`**。

这意味着所有 1.4 节的 Max 特征关键词拦截、1.2 节的余额耗尽改写、1.6 节的 Bedrock 签名识别，对 OpenAI 格式请求全部失效。Max 上游的错误原文会直接透传给客户端。

**建议**：在通用 relay handler 的 Claude 渠道错误路径中也调用 `NormalizeClaudeError`（或其等价逻辑）。需注意此时输出格式应保持 OpenAI 格式，只改写内容和状态码。

### 4.2 `RelayErrorHandler` 对 429/503 的预处理

`service/error.go` 的 `RelayErrorHandler` 在 defer 中对上游 429 和 503 统一替换为 `"Overloaded"` 文案，保持原状态码。这发生在 `NormalizeClaudeError` 之前。

**潜在问题**：如果上游 429/503 的原始 body 中包含需要关键词匹配的内容（如余额耗尽被上游包成 429），经过 `RelayErrorHandler` 后原始 body 被替换为 `"Overloaded"`，`NormalizeClaudeError` 拿到的 message 就只有 `"Overloaded"`，无法匹配关键词。

**实际影响**：429 和 503 本身在 retry 范围内会触发换渠道，且 `NormalizeClaudeError` 对 `"Overloaded"` 这个文案不会误匹配白名单，最终会透传这个 429/503。行为基本正确，但如果需要区分"真限流"和"伪限流（余额耗尽包成 429）"就会丢信息。

**建议**：暂不改动，但如果将来需要对 429/503 做更精细的分类（如标记账号不可用），需在 `RelayErrorHandler` 替换前先做关键词分类。

---

## 五、请求阶段校验（"静默放行"问题）

这组问题本质上是 **new-api 作为 API 网关的宽容设计** vs **严格模拟官方 API 行为** 的权衡。

### 5.1 不建议改动的（new-api 通用设计）

| 场景 | 当前行为 | 官方行为 | 不改原因 |
|---|---|---|---|
| 不传 `max_tokens` | 补默认值 → 200 | 400 | 这是 new-api 有意为之的兼容行为，大量用户依赖此特性。改成 400 会破坏现有用户。 |
| 未知字段 | 静默丢弃 → 200 | 400 `Extra inputs are not permitted` | Go `json.Unmarshal` 默认行为。加 `DisallowUnknownFields` 会影响所有格式的所有请求，且与 API 网关"向前兼容"的设计哲学矛盾。 |
| 缺少 `anthropic-version` | 补默认 `2023-06-01` → 200 | 400 | 同 max_tokens，是兼容设计。大量客户端不传此 header。 |
| 伪造 beta header | 透传给上游 → 取决于上游 | 400 | 网关不应校验 beta 值的合法性，留给上游判断是正确的。 |

### 5.2 可选改动的（仅 FCF 模式下严格化）

如果目标是让 FCF 渠道**完全**像官方 API，可以在 FCF 开启时增加以下校验：

| 场景 | 建议 | 优先级 |
|---|---|---|
| `temperature` 越界（官方范围 0~1） | FCF 模式下校验范围，越界返回 400 | P2 |
| `thinking.type=enabled` 不兼容模型 | FCF 模式下校验模型兼容性，不兼容返回 400 | P2 |
| `max_tokens` 缺失 | 可选：FCF 模式下不补默认，返回 400 | P3（风险高，可能破坏用户） |

**建议**：这组改动风险收益比低，且每个都需要维护一份模型兼容性表。除非有明确用户反馈说"FCF 模式下也被静默放行了，导致问题"，否则暂不改动。

---

## 六、真实代码缺陷（非设计选择）

这些是代码 bug，无论是否做归一化整改都应修复：

### 6.1 空 messages → 500（应为 400）

`relay/helper/valid_request.go` 中 `GetAndValidateClaudeRequest` 对空 messages 返回 `errors.New("field messages is required")`，在 `controller/relay.go` 被 `types.NewError()` 包装时默认状态码为 500。

**影响**：500 在重试范围内，SDK 会反复重试一个永远不会成功的请求。

**修复方向**：显式指定 400 状态码。

### 6.2 `max_tokens: -1` → 500 + Go 类型泄漏（应为 400 + 友好文案）

`ClaudeRequest.MaxTokens` 是 `*uint`，JSON 解码 `-1` 时产生 Go 内部错误信息 `"cannot unmarshal number -1 into Go struct field ClaudeRequest.max_tokens of type uint"`。

**影响**：(1) 500 触发无意义重试；(2) 暴露 Go 内部类型名。

**修复方向**：捕获 unmarshal 错误，改写为 400 + 用户友好文案（如 `"max_tokens: must be a positive integer"`）。

### 6.3 所有校验错误的状态码默认值问题

`types.NewError()` 在不显式指定状态码时默认 500。当前 `Relay()` 函数中多处用 `types.NewError(err, types.ErrorCodeXxx)` 包装校验错误，都会默认 500。

**修复方向**：对所有校验类错误统一使用 `types.NewErrorWithStatusCode(..., http.StatusBadRequest)` 或 `types.ErrOptionWithStatusCode(http.StatusBadRequest)`。

---

## 七、优先级总结

| 优先级 | 项目 | 类型 |
|---|---|---|
| **P0** | 4.1 OpenAI 格式走 Claude 渠道不经过 error normalization | 拦截绕过 |
| **P0** | 1.4 补充 12 条 MAX 特征关键词 | 信息泄漏 |
| **P0** | 6.1 空 messages 返回 500 → 400 | 代码缺陷 |
| **P0** | 6.2 max_tokens: -1 返回 500 + 类型泄漏 → 400 + 友好文案 | 代码缺陷 |
| **P1** | 1.6 补充 4 条 Bedrock 死上游签名关键词 | 拦截缺失 |
| **P1** | 2.2 整池失败 529 响应追加 `Retry-After` header | 协议对齐 |
| **P1** | 3.1 distributor 层对 Claude 格式请求返回 Claude 格式错误 | 格式错误 |
| **P2** | 2.3 模型不存在 503 → 404（需分发层改造） | 语义对齐 |
| **P2** | 1.2 余额耗尽中文关键词 / 分档调整 | 小幅补充 |
| **P2** | 5.2 FCF 模式下 temperature/thinking 严格校验 | 可选严格化 |
| **P3** | 3.2 响应 header 白名单微调 | 锦上添花 |
| **不改** | 5.1 max_tokens 补默认 / 未知字段丢弃 / anthropic-version 补默认 | new-api 通用设计 |
