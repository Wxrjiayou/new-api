# Max 渠道响应归一化方案（响应头 + 响应体）

> 基于 `max-vs-official-key-signature.md` 的取证结论。
> 目标：下游客户收到的响应字节（头 + 体），在格式层面无法区分背后是 Max 订阅渠道还是官方 API Key 渠道。
> 日期：2026-09-21

---

## 1. 改造总览

取证结论（引自原文档）：

| 层    | 非流式差异                                                               | 流式差异                                            |
| ---- | ------------------------------------------------------------------- | ----------------------------------------------- |
| 响应头  | **有差别**：Max 通常无 `anthropic-*`，官 Key 有完整的 org/workspace/ratelimit 头族 | **无差别**：两边都没有 `anthropic-*` 头（§3.1 实测 7 路抓包确认）  |
| 响应体  | **只差 1 个字段**：`usage.iterations[]`                                   | **只差 1 个字段**：`message_delta.usage.iterations[]` |
| 错误文案 | Max 会泄漏订阅制原文、中转自身文案、上游余额                                            | 同                                               |

改造分三层：

| 层    | 方案                           | 适用范围 |
| ---- | ---------------------------- | ---- |
| 响应头  | 拦截上游头 + 注入合成头（非流式）；拦截泄漏头（流式） | §2   |
| 响应体  | 删除 `usage.iterations[]`      | §3   |
| 错误文案 | 分档拦截/改写/透传                   | §4   |

---

## 2. 响应头改造

### 2.1 非流式响应头

非流式是头层差异的唯一战场。方案：**拦截所有上游头 + 注入合成头**，让下游看到的头形状与直连 `api.anthropic.com` 一致。

#### 2.1.1 必须拦截/删除的上游头

不做黑名单（追不完），做**白名单**：只有 §2.1.3 列出的头才能出现在下游响应中，其余一律丢弃。

以下是取证中实际在漏的头，列出来供审计和测试对照：

| 头                                                                                                                                                                                                                                       | 来源                 | 泄漏内容                           | 严重程度   |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------ | ------------------------------ | ------ |
| `anthropic-ratelimit-unified-*`（13 个：status / reset / representative-claim / 5h-status / 5h-utilization / 5h-reset / 7d-status / 7d-utilization / 7d-reset / fallback / fallback-percentage / overage-status / overage-disabled-reason） | Max 侧漏出            | **订阅身份铁证**，只有订阅号才有此头族，漏一个即全盘暴露 | **致命** |
| `x-client-request-id`（裸 UUID）                                                                                                                                                                                                           | Max 侧 8 渠道 / 116 次 | 当前最强的 Max 渠道指纹                 | 高      |
| `anthropic-organization-id`                                                                                                                                                                                                             | 官 Key 侧            | 上游真实组织身份                       | 高      |
| `anthropic-workspace-id`                                                                                                                                                                                                                | 官 Key 侧            | 上游真实 workspace                 | 高      |
| `anthropic-ratelimit-*` classic（12 个：requests/tokens/input-tokens/output-tokens × limit/remaining/reset）                                                                                                                                | 官 Key 侧            | 上游真实额度水位                       | 高      |
| `x-upstream-request-id`                                                                                                                                                                                                                 | 官 Key 侧            | 明说有上游                          | 中      |
| `traceresponse`                                                                                                                                                                                                                         | 官 Key 侧            | 上游 trace id                    | 中      |
| `eo-cache-status` / `eo-log-uuid`                                                                                                                                                                                                       | Max 侧 30 次         | 腾讯 EdgeOne CDN 指纹              | 中      |
| `l-request-id` / `l-via` / `l-version`                                                                                                                                                                                                  | Max 侧 10 次         | 某中转软件版本指纹                      | 中      |
| `x-new-api-version` / `x-oneapi-request-id`                                                                                                                                                                                             | 两侧                 | 中转软件身份                         | 低      |
| `pragma` / `expires` / `nel` / `report-to`                                                                                                                                                                                              | 两侧                 | CDN 配置差异                       | 低      |
| `server`（`openresty` / `nginx/1.28.3 (Ubuntu)`）                                                                                                                                                                                         | 两侧                 | 中转软件 + 发行版版本                   | 低      |
| 上游原始 `x-request-id` / `request-id`                                                                                                                                                                                                      | 两侧                 | 上游请求 ID 格式暴露链路                 | 中      |

#### 2.1.2 必须注入的合成头

**身份头**：

| 头                           | 生成方式                                     | 说明                       |
| --------------------------- | ---------------------------------------- | ------------------------ |
| `request-id`                | `req_` + 随机 base58（24 字符）                | 官方格式是 `req_` 前缀，不是裸 UUID |
| `anthropic-organization-id` | `UUIDv5(固定命名空间, 下游APIKey)`               | 同一下游 key 每次请求稳定一致，不暴露上游  |
| `anthropic-workspace-id`    | `UUIDv5(固定命名空间, 下游APIKey + "workspace")` | 官方侧"多数有"，可对部分请求省略以更真实    |

**限流头**（基于下游客户自身配额的本地状态机）：

| 头                                             | 值的来源                                           |
| --------------------------------------------- | ---------------------------------------------- |
| `anthropic-ratelimit-requests-limit`          | 下游客户配置的 RPM 上限                                 |
| `anthropic-ratelimit-requests-remaining`      | limit − 当前 1 分钟窗口内该客户的实际请求数                    |
| `anthropic-ratelimit-requests-reset`          | 当前窗口结束时间，RFC 3339 格式（如 `2026-09-21T12:01:00Z`） |
| `anthropic-ratelimit-tokens-limit`            | 下游客户配置的 TPM 上限                                 |
| `anthropic-ratelimit-tokens-remaining`        | limit − 当前窗口内该客户的实际 token 消耗                   |
| `anthropic-ratelimit-tokens-reset`            | 同上                                             |
| `anthropic-ratelimit-input-tokens-limit`      | 下游客户配置的输入 TPM 上限                               |
| `anthropic-ratelimit-input-tokens-remaining`  | limit − 当前窗口实际输入 token 消耗                      |
| `anthropic-ratelimit-input-tokens-reset`      | 同上                                             |
| `anthropic-ratelimit-output-tokens-limit`     | 下游客户配置的输出 TPM 上限                               |
| `anthropic-ratelimit-output-tokens-remaining` | limit − 当前窗口实际输出 token 消耗                      |
| `anthropic-ratelimit-output-tokens-reset`     | 同上                                             |

**限流状态机要点**：

- `remaining` 在**每次响应返回后**递减（请求数 −1，token 数从 `usage` 读取）
- `reset` 对齐到分钟整点，窗口到期后 `remaining` 重置为 `limit`
- 数字来自中转自身的 per-key 计数器，不依赖上游——既真实又对客户有用
- 如果下游客户没有配置独立的 RPM/TPM 上限，使用一组合理默认值（参照 Anthropic 公开的 Build tier：RPM 1000 / TPM 400000 / 输入 TPM 320000 / 输出 TPM 80000）

**安全头**（固定值，官方 API 返回的标准安全头）：

| 头                           | 固定值                                            |
| --------------------------- | ---------------------------------------------- |
| `content-security-policy`   | `default-src 'none'; frame-ancestors 'none'`   |
| `strict-transport-security` | `max-age=31536000; includeSubDomains; preload` |
| `x-robots-tag`              | `none`                                         |

#### 2.1.3 最终白名单（非流式下游响应中允许出现的全部头）

```
# 上游透传
content-type
content-length / transfer-encoding
cache-control
anthropic-version            （上游回了就转，没有不补）
x-should-retry
retry-after

# 中转生成
request-id                   （req_ 前缀，中转生成）

# 合成注入
anthropic-organization-id
anthropic-workspace-id
anthropic-ratelimit-requests-limit
anthropic-ratelimit-requests-remaining
anthropic-ratelimit-requests-reset
anthropic-ratelimit-tokens-limit
anthropic-ratelimit-tokens-remaining
anthropic-ratelimit-tokens-reset
anthropic-ratelimit-input-tokens-limit
anthropic-ratelimit-input-tokens-remaining
anthropic-ratelimit-input-tokens-reset
anthropic-ratelimit-output-tokens-limit
anthropic-ratelimit-output-tokens-remaining
anthropic-ratelimit-output-tokens-reset
content-security-policy
strict-transport-security
x-robots-tag
```

**白名单之外的任何头一律丢弃。**

#### 2.1.4 为什么不从 thinking signature 解码 org UUID

原文档 §5.4 分析过"从 signature 解码 org UUID 填入头"的方案。虽然技术可行，但存在三个问题：

1. **暴露上游真实账号身份**：signature 里的 UUID 是上游订阅账号的真实组织 ID，放进头里等于主动泄漏
2. **非 thinking 请求无 signature 可解码**：不是所有请求都开 extended thinking，此时无来源
3. **跨请求不一致**：负载均衡池换了上游账号，signature 里的 UUID 会变，头里的值也得跟着变

**合成方案**（per 下游 key 派生 UUID）规避了以上全部问题，代价是 signature 内的真实 UUID 与头里的合成 UUID 不匹配——但需要专门 base64 解码 signature 才能发现，属于可接受的残留风险。

---

### 2.2 流式响应头

取证结论（§3.1）：**流式下两侧都没有 `anthropic-*` 头**，7 路抓包确认。中转在 SSE 路径上普遍不转发上游头。

因此流式响应头**不注入合成 `anthropic-*` 头**。只需确保不泄漏以下头：

| 必须拦截的头                                      | 原因       |
| ------------------------------------------- | -------- |
| `x-client-request-id`                       | Max 渠道指纹 |
| `eo-cache-status` / `eo-log-uuid`           | CDN 指纹   |
| `l-request-id` / `l-via` / `l-version`      | 中转软件指纹   |
| `x-new-api-version` / `x-oneapi-request-id` | 中转软件身份   |
| `server`                                    | 中转软件版本   |

**流式白名单**（允许出现的全部头）：

```
content-type                 （text/event-stream）
cache-control                （no-cache）
transfer-encoding
x-accel-buffering            （no，防止 nginx 缓冲 SSE）
```

其余丢弃。中转可自行生成一个 `request-id: req_...`，但不是必须（官方侧流式样本也未见此头）。

---

## 3. 响应体改造

### 3.1 非流式：删除 `usage.iterations[]`

**触发条件**：HTTP 2xx，`Content-Type: application/json`，响应体 JSON 中 `usage.iterations` 存在。

**操作**：JSON 解析响应体 → 删除 `usage` 对象中的 `iterations` 键 → 重新序列化。

**改造前**（Max 侧真实样本）：

```json
{
  "model": "claude-opus-5",
  "id": "msg_011CfFtz…",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "thinking", "thinking": "", "signature": "CAIStQIKpgEIEhgC…"}],
  "stop_reason": "max_tokens",
  "stop_sequence": null,
  "stop_details": null,
  "usage": {
    "input_tokens": 8,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0,
    "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0},
    "output_tokens": 16,
    "output_tokens_details": {"thinking_tokens": 0},
    "service_tier": "standard",
    "inference_geo": "global",
    "iterations": [
      {
        "input_tokens": 8,
        "output_tokens": 16,
        "cache_read_input_tokens": 0,
        "cache_creation_input_tokens": 0,
        "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0}
      }
    ]
  }
}
```

**改造后**：

```json
{
  "model": "claude-opus-5",
  "id": "msg_011CfFtz…",
  "type": "message",
  "role": "assistant",
  "content": [{"type": "thinking", "thinking": "", "signature": "CAIStQIKpgEIEhgC…"}],
  "stop_reason": "max_tokens",
  "stop_sequence": null,
  "stop_details": null,
  "usage": {
    "input_tokens": 8,
    "cache_creation_input_tokens": 0,
    "cache_read_input_tokens": 0,
    "cache_creation": {"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": 0},
    "output_tokens": 16,
    "output_tokens_details": {"thinking_tokens": 0},
    "service_tier": "standard",
    "inference_geo": "global"
  }
}
```

**不可改动的字段**（两组都有，删了反而露馅）：

| 字段                                         | Max 出现率 | 官 Key 出现率 |
| ------------------------------------------ | ------- | --------- |
| `service_tier`                             | 212/212 | 126/126   |
| `inference_geo`                            | 212/212 | 108/126   |
| `output_tokens_details.thinking_tokens`    | 212/212 | 101/126   |
| `cache_creation.ephemeral_5m_input_tokens` | 212/212 | 126/126   |
| `cache_creation.ephemeral_1h_input_tokens` | 212/212 | 126/126   |

**计费安全**：计费逻辑只读 `input_tokens` / `output_tokens` / `cache_read_input_tokens` / `cache_creation_input_tokens`。`iterations[]` 是重复记账明细，删除不影响计费。实施前须确认本系统计费代码路径中没有读取 `iterations`。

---

### 3.2 流式：改写 `message_delta` 事件

**触发条件**：SSE 事件 `event: message_delta`，其 `data:` JSON 中 `usage.iterations` 存在。

**关键发现**（§3.2）：`iterations[]` **只出现在 `message_delta`**，不出现在 `message_start`。因此只需改写这一个事件类型。

**操作**：逐事件扫描 SSE 流 → 当事件类型为 `message_delta` 时解析 `data:` JSON → 若 `usage.iterations` 存在则删除 → 重新序列化替换 `data:` 行 → 其余事件原样透传。

**改造前**（Max 侧真实 SSE）：

```
event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":17,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":27,"output_tokens_details":{"thinking_tokens":17},"iterations":[{"input_tokens":17,"output_tokens":27,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"message"}]},"context_management":{"applied_edits":[]}}
```

**改造后**：

```
event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":17,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":27,"output_tokens_details":{"thinking_tokens":17}},"context_management":{"applied_edits":[]}}
```

**实现要点**：

1. **必须 JSON 解析后重序列化**，不能用正则——`iterations` 数组内嵌套对象，正则切不干净
2. **只处理 `message_delta`**，`message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `ping` / `message_stop` 全部原样透传
3. **JSON 尾部空白填充会丢失**——不是区分信号（填充为 0 只说明经过重序列化，两侧直连时都有填充）
4. **`context_management` 字段保留**——官方字段，两组都有

---
