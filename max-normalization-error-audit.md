# Max 渠道归一化审计：现状、缺口与改造方案

> 基于 `max-vs-official-key-signature.md` 取证结论 + 代码审计（2026-09-22）
> 适用范围：仅对开启了**强制 Claude 格式化**（`force_claude_format`）的 Anthropic 渠道生效。
> 开关位置：渠道管理 → 创建/编辑渠道（Anthropic 类型） → 高级设置 → 渠道额外设置 → 强制 Claude 格式化

---

## 1. 现有实现

`force_claude_format` 开关已存在且具备以下能力：

### 1.1 响应体归一化（已实现）

**文件**：`relay/channel/claude/claude_normalize.go`

| 操作 | 非流式 | 流式 |
|---|---|---|
| 删除 `usage.iterations[]` | `normalizeClaudeBody` :93 | `normalizeClaudeStreamEvent` :117（仅 `message_delta`） |
| 删除 `context_management` | :94 | :116（`message_delta`）, :108（`message_start`） |
| `inference_geo` 归一为 `"global"` | :95-97 | :109-111（`message_start`） |
| 补 `container: null` | :98-100 | :112-114（`message_start`）, :119-121（`message_delta`） |
| 删除 `message_delta.usage.cache_creation` | — | :118 |

### 1.2 响应头归一化（已实现）

**非流式**（`writeClaudeNormalizedResponse` :126-163）：
- 白名单过滤上游头（:23-29，仅放行 `content-type` / `cache-control` / `anthropic-version` / `x-should-retry` / `retry-after`）
- 注入 `request-id: req_` + base58 随机 ID（:139）
- 注入 `anthropic-organization-id`（:143，UUIDv5 派生自下游 token key）
- 注入 `anthropic-workspace-id`（:144，同上 + "workspace" 后缀）
- 注入安全头 CSP / HSTS / x-robots-tag（:147-149）
- 注入合成 ratelimit 头（:151，调用 `setClaudeRatelimitHeaders`）

**流式**（`stream_scanner.go:144`）：
- `ForceClaudeFormat` 启用时跳过 `copyCodexSSEHeaders`，不复制上游 SSE 头
- 流式白名单（:31-36）：仅放行 `content-type` / `cache-control` / `transfer-encoding` / `x-accel-buffering`

### 1.3 合成限流状态机（已实现）

**文件**：`relay/channel/claude/claude_ratelimit.go`

- per-token 滑动窗口计数器（分钟级），跟踪 requests / tokens / input-tokens / output-tokens
- 默认 limit：RPM 1000 / TPM 400000 / 输入 TPM 320000 / 输出 TPM 80000
- `remaining` = limit − 当前窗口已用量，`reset` = 下一分钟整点（RFC 3339）
- 定期清理过期窗口（2 分钟间隔，5 分钟阈值）

### 1.4 调用路径

| 路径 | 归一化入口 | 头注入 |
|---|---|---|
| 非流式成功 | `relay-claude.go:255` → `normalizeClaudeBody` → :272 `writeClaudeNormalizedResponse` | 在 `writeClaudeNormalizedResponse` 内 |
| 流式成功 | `relay-claude.go:114` → `normalizeClaudeStreamEvent` | `stream_scanner.go:144` 跳过上游头 |
| **非流式错误** | **不经过归一化** — `claude_handler.go:210-214` 直接走 `RelayErrorHandler` → 返回 | **不注入合成头** |
| **流式错误** | **不经过归一化** — 流式中途错误由 scanner 处理 | — |

---

## 2. 缺口：错误响应路径完全绕过归一化

当上游返回非 200 时（`claude_handler.go:210`），代码走：

```go
if httpResp.StatusCode != http.StatusOK {
    newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
    service.ResetStatusCode(newAPIError, statusCodeMappingStr)
    return newAPIError
}
```

`RelayErrorHandler`（`service/error.go:89-151`）的处理：
- **429 / 503**：替换消息为 `"The upstream server is temporarily unavailable, please try again later"`
- **其他所有状态码**：解析错误体后**原样透传**

这意味着 §5.3 A~G 档描述的所有错误文案，除 429/503 外，全部穿透。

---

## 3. 改造方案

### 3.0 适用范围

所有改造**仅对 `force_claude_format` 启用的渠道生效**，通过 `shouldApplyClaudeNormalize(info)` 判定。

对于全局生效的改动（D-1 源头改、F-1 替换文案），不受此开关限制。

### 3.1 全局改动（所有渠道）

#### G-0：F-1 修正 429/503 替换文案

**文件**：`service/error.go:87`

| 当前 | 改为 |
|---|---|
| `"The upstream server is temporarily unavailable, please try again later"` | `"Overloaded"` |

同步修改 `controller/relay.go:614-619` 中 task 错误的相同替换。

**理由**：`upstream` 一词对所有渠道都是泄漏，不限于 Claude。Anthropic 官方 429 文案是 `"Overloaded"`。

---

#### G-1：D-1 去掉分组名 / distributor 标识

**文件**：`middleware/distributor.go`, `controller/relay.go`, `i18n/locales/`

| 当前 | 改为 |
|---|---|
| `分组 {{Group}} 下模型 {{Model}} 无可用渠道（distributor）` | `The model is currently unavailable, please try again later` |
| `获取分组 {{Group}} 下模型 {{Model}} 的可用渠道失败（distributor）：{{Error}}` | 同上 |
| `获取分组 %s 下模型 %s 的可用渠道失败（retry）: %s` | 同上 |
| `分组 %s 下模型 %s 的可用渠道不存在（retry）` | 同上 |

分组名、`(distributor)`、`(retry)` 标识、内部 error 细节只写入 `logger.LogWarn`，不返回给客户。

---

### 3.2 Claude 格式化专属改动（仅 `force_claude_format` 渠道）

在 `RelayErrorHandler` 返回后、错误响应发给下游之前，插入一个 Claude 格式化错误拦截层。判定流程：

```
RelayErrorHandler 返回 newAPIError
   │
   ▼
shouldApplyClaudeNormalize(info)？ ──否──→ 走原有逻辑
   │ 是
   ▼
HTTP 200？ ──是──→ 不走文案判定
   │ 否
   ▼
G 档白名单命中？ ──是──→ 原样返回（最高优先级）
   │ 否
   ▼
A~E 档内容匹配
   │ 命中
   ▼
替换错误体 + 标记可重试（走 new-api 自有重试循环，保留亲和性）
   │ 未命中
   ▼
F 档匹配？ ──是──→ 保留状态码，只替换 message 文本
   │ 否
   ▼
原样返回
```

---

#### 3.2.1 G 档：白名单（原样透传，最高优先级）

**匹配方式**：小写包含，任一命中即放行。

| 关键词 | 说明 |
|---|---|
| `input is too long` | 上下文超长 |
| `too many tokens` | token 超限 |
| `token limit` | 同上 |
| `context length` | 上下文长度限制 |
| `maximum context` | 同上 |
| `exceeds the model` | 超出模型限制 |
| `content length exceeds` | 内容长度超限 |
| `prompt is too long` | 提示词太长 |
| `request too large` | 请求体太大 |
| `payload too large` | 同上 |
| `max_tokens` | max_tokens 参数错误 |
| `invalid signature` | thinking signature 校验失败 |
| `does not support oneof` | schema 不支持 |
| `does not support allof` | 同上 |
| `does not support anyof` | 同上 |
| `tool_result block` | tool_result 格式错误 |
| `image exceeds` | 图片尺寸超限 |

**命中后**：不修改，不拦截，原样返回给下游。

---

#### 3.2.2 A 档：订阅制限额 → 替换 + 重试

**匹配方式**：小写包含 + 正则。

| 匹配规则 | 类型 |
|---|---|
| `switch to another model` | 包含 |
| `spend limit` | 包含 |
| `shared budget` | 包含 |
| `usage credits` | 包含 |
| `limit reached` | 包含 |
| `(?i)(?:reached\|hit) your .{0,40}limit` | 正则 |

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

状态码：`529`

**重试行为**：将错误标记为可重试（修改 `newAPIError.StatusCode` 为 429 或 529，使其落入默认重试范围 409-499 / 500-599）。重试走 new-api 自有重试循环（`controller/relay.go:184-255`），`getChannel()` 在每次重试时选择不同优先级渠道，跨请求亲和性不受影响。

---

#### 3.2.3 B 档：第三方应用 / extra usage → 替换 + 重试

**匹配方式**：小写包含。

| 匹配规则 |
|---|
| `extra usage` |
| `not your plan limits` |
| `claude.ai/settings/usage` |
| `with the claude pro plan` |
| `may not be enabled for your organization` |
| `restricted by your organization` |
| `all upstreams failed` |

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

状态码：`529`

**重试行为**：同 A 档。

---

#### 3.2.4 C 档：余额/额度耗尽（仅上游穿透部分）→ 替换 + 重试

**无条件命中**：`HTTP 402`（不看正文，但须先过 G 档白名单）

**小写包含匹配**（非 200）：

| 匹配规则 | 说明 |
|---|---|
| `usage limit` | |
| `wholesale credit` | |
| `add credits` | |
| `add additional credits` | |
| `top up` | |
| `topup` | |
| `out of credit` | |
| `credit balance is too low` | Anthropic 原生欠费文案 |
| `purchase credits` | |
| `quota exceeded` | |
| `payment required` | |
| `operation not allowed` | 中转统一拒绝文案 |

**条件匹配**（**仅非 400 时**，防止误吃 `insufficient tokens in prompt`）：

| 匹配规则 |
|---|
| `insufficient` |
| `billing` |

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

状态码：`529`

**重试行为**：同 A 档。

---

#### 3.2.5 D-2 档：上游中转自身文案 → 替换 + 重试

**匹配方式**：小写包含。

| 匹配规则 | 说明 |
|---|---|
| `stopped locally before forwarding` | 上游中转本地守卫拦截 |
| `refusal guard` | 同上的第二锚点 |

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

状态码：`529`

**重试行为**：同 A 档。换号后大概率能通过（上游中转的守卫是按号判定的）。

---

#### 3.2.6 E 档：限流被压成 400 → 替换 + 重试

**匹配方式**：**仅 400 时匹配**。

| 匹配规则 | 类型 |
|---|---|
| `upstream rate limit exceeded` | 包含 |
| `(?i)exceed(?:ed\|s)? your (?:organization\|account\|org)'?s? rate limit` | 正则 |

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "Overloaded"
  }
}
```

状态码：`429`（保持限流语义，让下游客户端可以正确退避）

**重试行为**：429 已在默认重试范围内（409-499），自动触发重试。

---

#### 3.2.7 F-2 档：nginx HTML 错误页 → 替换为 JSON

**匹配方式**：响应体（去前导空白后）以 `<html` 或 `<!doctype` 开头（小写）。

**命中后替换为**：

```json
{
  "type": "error",
  "error": {
    "type": "api_error",
    "message": "Internal server error"
  }
}
```

状态码：保留原值（通常 502）。

**不标记重试**：502 已在默认重试范围内（500-599），自动触发。

---

## 4. 替换总表

| 档 | 匹配条件 | 替换 body | 替换状态码 | 重试 | 仅 `force_claude_format` |
|---|---|---|---|---|---|
| **G** | 白名单关键词命中 | 不替换 | 不改 | 不改 | 是 |
| **A** | 订阅限额关键词 | `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` | `529` | 标记可重试 | 是 |
| **B** | extra usage 关键词 | 同上 | `529` | 标记可重试 | 是 |
| **C** | 余额类关键词 / 402 | 同上 | `529` | 标记可重试 | 是 |
| **D-1** | 我们自己生成的分组名 | `The model is currently unavailable, please try again later` | 不改 | 不改 | **否（全局）** |
| **D-2** | `refusal guard` / `stopped locally` | `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` | `529` | 标记可重试 | 是 |
| **E** | 限流压成 400 的关键词（仅 400） | `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` | `429` | 已在重试范围 | 是 |
| **F-1** | 现有 429/503 替换文案 | `"Overloaded"` | 不改 | 不改 | **否（全局）** |
| **F-2** | HTML 错误页 | `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}` | 不改 | 已在重试范围 | 是 |

---

## 5. 实施位置

### 5.1 全局改动

| 改动 | 文件 | 行号 | 具体修改 |
|---|---|---|---|
| F-1 替换文案 | `service/error.go` | 87 | `const upstreamUnavailableMessage = "Overloaded"` |
| F-1 task 路径 | `controller/relay.go` | 614-619 | 同上 |
| D-1 分组名 | `middleware/distributor.go` | 148, 158 | 去掉 `{{Group}}`、`(distributor)` |
| D-1 重试路径 | `controller/relay.go` | 316, 319 | 去掉 `%s`（分组名）、`(retry)` |
| D-1 i18n | `i18n/locales/en.yaml` | 274 | 同步修改英文模板 |
| D-1 i18n | `i18n/locales/zh-CN.yaml` | 275 | 同步修改中文模板 |

### 5.2 Claude 格式化专属改动

**插入点**：在 `claude_handler.go:210-214` 的错误处理路径中，`RelayErrorHandler` 返回后、`return newAPIError` 之前，当 `shouldApplyClaudeNormalize(info)` 为 true 时，调用新的 Claude 错误格式化函数。

伪代码：

```go
if httpResp.StatusCode != http.StatusOK {
    newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
    service.ResetStatusCode(newAPIError, statusCodeMappingStr)
    // ---- 新增 ----
    if shouldApplyClaudeNormalize(info) {
        newAPIError = normalizeClaudeError(newAPIError, httpResp.StatusCode)
    }
    // ---- 结束 ----
    return newAPIError
}
```

`normalizeClaudeError` 函数实现 §3.2 的判定流程（G → A → B → C → D-2 → E → F-2），建议放在 `relay/channel/claude/claude_normalize.go` 中与现有归一化逻辑共处。

---

## 6. 现有代码索引

| 模块 | 文件 | 功能 |
|---|---|---|
| 开关定义 | `relaykit/dto/channel_settings.go:20` | `ForceClaudeFormat bool` |
| 开关判定 | `relay/channel/claude/claude_normalize.go:165` | `shouldApplyClaudeNormalize()` |
| 响应体归一化 | `relay/channel/claude/claude_normalize.go:92-124` | `normalizeClaudeBody` / `normalizeClaudeStreamEvent` |
| 响应头归一化 | `relay/channel/claude/claude_normalize.go:126-163` | `writeClaudeNormalizedResponse` |
| 合成限流头 | `relay/channel/claude/claude_ratelimit.go:94-120` | `setClaudeRatelimitHeaders` |
| 流式头过滤 | `relay/helper/stream_scanner.go:144` | 跳过 `copyCodexSSEHeaders` |
| 错误处理入口 | `relay/claude_handler.go:210-214` | 非流式错误路径 |
| 429/503 拦截 | `service/error.go:87-98` | 状态码匹配 + 消息替换 |
| 重试循环 | `controller/relay.go:184-255` | 主重试循环 |
| 重试决策 | `controller/relay.go:331-361` | `shouldRetry()` |
| 渠道选择 | `model/channel_cache.go:114-207` | 优先级加权随机 |
| 渠道亲和 | `service/channel_affinity.go` | 粘性路由 |
| 自动禁用关键词 | `setting/operation_setting/operation_setting.go:8-16` | 渠道禁用（不拦截文案） |
| 前端开关 | `web/src/features/channels/components/drawers/channel-mutate-drawer.tsx:4101-4126` | UI toggle |
