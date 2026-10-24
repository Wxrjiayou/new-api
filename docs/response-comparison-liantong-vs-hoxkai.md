# Claude Max 渠道响应格式对比

> 对比站点：`us.liantongapi.com`（我们）vs `api.hoxkai.top`（对比）
> 模型：`claude-opus-4-6`，开启 thinking
> 测试日期：2026-09-28

---

## 一、非流式响应 body 完整对比

### 我们的站点

```json
{
    "model": "claude-opus-4-6",
    "id": "msg_011CfUtMavQZFtppAt6eKj5k",
    "type": "message",
    "role": "assistant",
    "content": [
        {
            "type": "thinking",
            "thinking": "5",
            "signature": "EowCCqgBCBIYAi..."
        },
        {
            "type": "text",
            "text": "2 + 3 = **5**"
        }
    ],
    "container": null,
    "stop_reason": "end_turn",
    "stop_sequence": null,
    "stop_details": null,
    "usage": {
        "input_tokens": 43,
        "cache_creation_input_tokens": 0,
        "cache_read_input_tokens": 0,
        "output_tokens": 22,
        "service_tier": "standard",
        "inference_geo": "global",
        "output_tokens_details": {
            "thinking_tokens": 7
        }
    }
}
```

### 对比站点

```json
{
    "model": "claude-opus-4-6",
    "id": "msg_011CfUtMiaHNj8d35vSUCtVk",
    "type": "message",
    "role": "assistant",
    "content": [
        {
            "type": "thinking",
            "thinking": "5",
            "signature": "EvQBCpABCBIYAi..."
        },
        {
            "type": "text",
            "text": "2 + 3 = **5**"
        }
    ],
    "container": null,
    "stop_reason": "end_turn",
    "stop_sequence": null,
    "stop_details": null,
    "usage": {
        "input_tokens": 43,
        "cache_creation_input_tokens": 0,
        "cache_read_input_tokens": 0,
        "cache_creation": {                          // ← Max 特有，泄漏
            "ephemeral_5m_input_tokens": 0,
            "ephemeral_1h_input_tokens": 0
        },
        "output_tokens": 22,
        "output_tokens_details": {
            "thinking_tokens": 7
        },
        "service_tier": "standard",
        "inference_geo": "not_available"             // ← 原值泄漏，未改写
    },
    "diagnostics": null                              // ← Max 特有，泄漏
}
```

### 逐字段差异表

| 字段 | 我们 | 对比站 | 官方 API | 谁更好 |
|------|------|--------|----------|--------|
| `container: null` | 有 | 有 | 有 | 平 |
| `stop_details: null` | 有 | 有 | 无此字段 | 均泄漏 |
| `usage.cache_creation` | **已剥离** | 泄漏 `{ephemeral_5m:0,...}` | 无此字段 | 我们 |
| `usage.inference_geo` | 改写为 `"global"` | 原值 `"not_available"` | `"global"` 或无 | 我们 |
| `diagnostics` | **已剥离** | 泄漏 `null` | 无此字段 | 我们 |
| `usage.iterations` | **已剥离** | 未观测到 | 无此字段 | 平 |

---

## 二、流式响应完整对比

### 我们的站点

```
event: message_start
data: {"message":{"container":null,"content":[],"id":"msg_...","model":"claude-opus-4-6",
  "role":"assistant","stop_details":null,"stop_reason":null,"stop_sequence":null,
  "type":"message","usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,
  "inference_geo":"global","input_tokens":43,"output_tokens":1,"service_tier":"standard"}},
  "type":"message_start"}

event: content_block_start
data: {"type":"content_block_start","index":0,
  "content_block":{"type":"thinking","thinking":"","signature":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,
  "delta":{"type":"thinking_delta","thinking":"5","estimated_tokens":null}}
                                                     ↑ 多余字段

event: content_block_delta
data: {"type":"content_block_delta","index":0,
  "delta":{"type":"signature_delta","signature":"EowCCqgBCBIYAi..."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,
  "content_block":{"type":"text","text":""}}

event: content_block_delta (x N)
data: {"type":"content_block_delta","index":1,
  "delta":{"type":"text_delta","text":"2 + 3 = **5**"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta",
  "delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"container":null},
  "usage":{"input_tokens":43,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,
  "output_tokens":22,"output_tokens_details":{"thinking_tokens":7}}}

event: message_stop
data: {"type":"message_stop"}
```

### 对比站点

```
event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-4-6","id":"msg_...",
  "type":"message","role":"assistant","content":[],"container":null,
  "stop_reason":null,"stop_sequence":null,"stop_details":null,
  "usage":{"input_tokens":43,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,
  "cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},
  "output_tokens":1,"service_tier":"standard","inference_geo":"not_available"},
  "diagnostics":null}}
   ↑ cache_creation 泄漏   ↑ inference_geo 原值   ↑ diagnostics 泄漏

event: content_block_start
data: {"type":"content_block_start","index":0,
  "content_block":{"type":"thinking","thinking":"","signature":""}}

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,
  "delta":{"type":"thinking_delta","thinking":"5"}}
                                    ↑ 无 estimated_tokens（更干净）

event: content_block_delta
data: {"type":"content_block_delta","index":0,
  "delta":{"type":"signature_delta","signature":"EvQBCpABCBIYAi..."}}

...（后续 text delta / message_delta / message_stop 结构类似）

event: message_delta
data: {"type":"message_delta",
  "delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"container":null},
  "usage":{"input_tokens":43,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,
  "output_tokens":22,"output_tokens_details":{"thinking_tokens":7}}}

event: message_stop
data: {"type":"message_stop"}
```

### 流式逐字段差异表

| 位置 | 字段 | 我们 | 对比站 | 官方 API | 谁更好 |
|------|------|------|--------|----------|--------|
| message_start | `message.usage.cache_creation` | **已剥离** | 泄漏 | 无此字段 | 我们 |
| message_start | `message.usage.inference_geo` | `"global"` | `"not_available"` | `"global"` | 我们 |
| message_start | `message.diagnostics` | **已剥离** | `null` 泄漏 | 无此字段 | 我们 |
| message_start | `message.stop_details` | `null` | `null` | 无此字段 | 均泄漏 |
| thinking_delta | `delta.estimated_tokens` | `null` 泄漏 | **无此字段** | 无此字段 | 对比站 |
| message_delta | `delta.stop_details` | `null` | `null` | 无此字段 | 均泄漏 |

---

## 三、Thinking Signature 结构解剖

### 3.1 我们是怎么处理 signature 的

**完全原样透传，不做任何修改。**

代码路径：

1. 上游（Max/Bedrock）返回的 JSON 中 `content[].signature` 是一个 base64 字符串
2. `relaykit/dto/claude.go:27` 定义了 `Signature string json:"signature,omitempty"`
3. `relay/channel/claude/relay-claude.go` 的流式/非流式处理中，signature 随 `ClaudeMediaMessage` 结构体一起反序列化再序列化
4. `normalizeClaudeBody()` 和 `normalizeClaudeStreamEvent()` 只删除 `diagnostics`/`cache_creation`/`input_transformations`/`iterations` 等字段，**从不触碰 content 数组内的 signature**
5. 因此 signature 的 base64 内容原封不动传给下游客户端

### 3.2 Signature 内部结构（protobuf 解码）

Signature 是 base64 编码的 protobuf 消息。用 `protoc --decode_raw` 解码后的结构：

```
field 2 {                              // 外层 wrapper
  field 1 {                            // 签名元数据
    1: 18                              // 版本/类型标识
    3: 2                               // 未知标识
    5: <64 字节>                       // ECDSA 公钥（所有请求完全相同）
    6: "claude-opus-4-6"               // 模型名
    7: 0                               // 未知 flag
    8: "thinking"                      // content block 类型
    11: "5082045e-d3a1-40b7-9ed6-..."  // conversation_id (UUID)
    14: <16 字节>                      // ⚠️ Max 特有：未知元数据
    17: 1                              // ⚠️ Max 特有：未知 flag
    21: 1790555096                     // 时间戳（秒级）
    22: 2                              // ⚠️ Max 特有：未知 flag
  }
  field 2: <12 字节>                   // HMAC/签名分片 1
  field 3: <12 字节>                   // HMAC/签名分片 2
  field 4: <48 字节>                   // ECDSA 签名主体
  field 5: <16 字节>                   // 签名分片 4 / nonce
}
field 3: 1                             // 外层版本
```

### 3.3 两站 signature 对比

| Protobuf field | 我们（273 字节） | 对比站（249 字节） | 说明 |
|---|---|---|---|
| `1.1` (版本) | 18 | 18 | 一致 |
| `1.3` (标识) | 2 | 2 | 一致 |
| `1.5` (公钥) | 64 字节 | 64 字节 | **完全相同** — 说明用的是同一组上游密钥 |
| `1.6` (模型) | `claude-opus-4-6` | `claude-opus-4-6` | 一致 |
| `1.7` (flag) | 0 | 0 | 一致 |
| `1.8` (类型) | `thinking` | `thinking` | 一致 |
| `1.11` (conv_id) | `5082045e-...` | `42de79c7-...` | 不同（不同会话） |
| **`1.14`** | **16 字节存在** | **不存在** | ⚠️ Max 特有字段，对比站已剥离 |
| **`1.17`** | **值 = 1** | **不存在** | ⚠️ Max 特有字段，对比站已剥离 |
| `1.21` (时间戳) | 1790555096 | 1790555098 | 不同（不同时间） |
| **`1.22`** | **值 = 2** | **不存在** | ⚠️ Max 特有字段，对比站已剥离 |
| `2-5` (签名) | 12+12+48+16 字节 | 12+12+48+16 字节 | 长度完全一致，内容不同（每次请求独立） |

### 3.4 关键发现

1. **签名是真实的**。两边的 ECDSA 公钥完全相同（说明来自同一个 Anthropic 签名服务），签名密码学部分（field 2-5）长度一致，结构一致。既不是伪造的，也不是互相抄的。

2. **我们的 signature 比对比站多 24 字节**（273 vs 249）。多出的是 protobuf field `1.14`（16 字节 blob）、`1.17`（varint 1）、`1.22`（varint 2）。这三个字段是 **Max 渠道特有的元数据**，官方直连 API 的 signature 中不包含。

3. **对比站解码了 protobuf 并删除了这 3 个字段**，然后重新编码回 base64。这不会影响签名验证——签名是对 thinking content 本身做的，元数据字段不参与签名验证的哈希输入。

4. **这 3 个字段是 Max 渠道的可识别指纹**。任何人 base64 解码 signature、检查 protobuf 结构就能发现有多余字段，从而判断响应来自 Max 订阅号而非官方 API。

### 3.5 conversation_id 泄漏风险

Signature 内的 `field 1.11`（UUID）是上游 Max 会话的 `conversation_id`。无论我们还是对比站，这个 UUID 都存在于 signature 中。

- 对比站没有剥离它（可能因为它是签名哈希输入的一部分，删除会破坏签名验证）
- 这个 UUID 本身不直接暴露 Max 账号信息，但如果同一个 conversation_id 被多个下游用户使用，理论上可以关联出共享上游账号

---

## 四、剩余差异汇总与优先级

| # | 差异 | 我们 | 对比站 | 建议修复 | 优先级 |
|---|------|------|--------|----------|--------|
| 1 | Signature 含 Max 特有 protobuf 字段 (14/17/22) | 泄漏 | 已剥离 | 解码 protobuf → 删除 field 14/17/22 → 重编码 | P1 |
| 2 | `stop_details: null` | 泄漏 | 泄漏 | 非流式/流式均删除 | P2 |
| 3 | 流式 `thinking_delta` 含 `estimated_tokens: null` | 泄漏 | 已剥离 | 流式归一化中删除 | P2 |
| 4 | `usage.cache_creation` | 已剥离 ✅ | 泄漏 | — | — |
| 5 | `diagnostics` | 已剥离 ✅ | 泄漏 | — | — |
| 6 | `inference_geo` | 改写 `"global"` ✅ | 原值泄漏 | — | — |

**结论：我们的 body 归一化比对比站更完善（4/5/6 对比站仍泄漏）。但 signature 内部的 protobuf 元数据剥离是对比站做了而我们没做的，这是当前最主要的差距。**
