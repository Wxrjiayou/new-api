# max 渠道 vs 官key —— 流式/非流式特征对照，以及把 max 归一成官key 的改造点

> 2026-09-21。证据 = 本地 `source_test_records` 519 条 `status='done'` + 同日现场实抓的真实流式 SSE。
> 局限：库里没有 `api.anthropic.com` 直连基线，所有"官key"都是经中转转出来的。

---

## 1. 先给结论

**差别比想象的小得多。**

| 层 | 非流式 | 流式 |
|---|---|---|
| 请求头 | 无差别 | 无差别 |
| 响应头 | **有差别**（max 通常没有 `anthropic-*`，官key 有；但 max 一旦没剥干净，`anthropic-ratelimit-unified-*` 会直接把订阅身份坐实，见 §2.2） | **无差别**（两边都没有 `anthropic-*`） |
| 响应体 | **只差 1 个字段**：`usage.iterations[]` | **只差 1 个字段**：同上，在 `message_delta` 里 |
| 错误文案 | 有差别（max 会漏订阅制原文） | 同 |
| 行为 | 有差别，且这才是真正的分水岭 | 同 |

官方 usage 字段（`service_tier` / `inference_geo` / `output_tokens_details`）**两组一个不缺**，形状完全一致。

所以"把 max 变成官key"在**格式层只要做 3 件事**，都很便宜；**行为层的差别格式化抹不平**，只能靠路由。

---

## 2. 非流式

### 2.1 请求头 —— 零区分度

这是我方探针自己发出去的，跟渠道无关：

| header | max 组 | 官key 组 |
|---|---|---|
| `x-api-key` | 314/314 | 2182/2182 |
| `anthropic-version: 2023-06-01` | 314/314 | 2182/2182 |
| `content-type: application/json` | 314/314 | 2182/2182 |

**别在请求头上找区别，没有。**

### 2.2 响应头 —— 这是非流式唯一的头层差异

max 侧真实样本（`claude-opus-5`）：

```
date / vary / connection: keep-alive / content-type: application/json
server: openresty
content-encoding: gzip
transfer-encoding: chunked
x-request-id: 05a2d734-....            ← 裸 UUID，不是 req_ 前缀
x-client-request-id: 9bdc54d1-....     ← 裸 UUID
referrer-policy: strict-origin-when-cross-origin
x-frame-options: DENY
x-content-type-options: nosniff
```
**一个 `anthropic-*` 头都没有。**

官key 侧真实样本（`claude-opus-4-7`）：

```
server: nginx/1.28.3 (Ubuntu)
cf-ray / cf-cache-status
request-id: req_011CdzoiPH6Wfq5aQrSKogzU        ← 官方 req_ 前缀
traceresponse: 00-16e751ead78b82d0b16aee6c8f096712-...
x-robots-tag: none
content-security-policy: default-src 'none'; frame-ancestors 'none'
strict-transport-security: max-age=31536000; includeSubDomains; preload
anthropic-organization-id: 69085adc-0fb0-49af-8caf-51210bb8ddaa
anthropic-workspace-id: wrkspc_01F9tQU73nf3abse3so92oXj
anthropic-ratelimit-requests-limit: 5000
anthropic-ratelimit-requests-remaining: 4999
anthropic-ratelimit-requests-reset: 2026-09-21T11:11:35Z
anthropic-ratelimit-tokens-limit: 6000000
anthropic-ratelimit-tokens-remaining: 6000000
anthropic-ratelimit-tokens-reset: 2026-09-21T11:11:35Z
anthropic-ratelimit-input-tokens-limit: 5000000
anthropic-ratelimit-input-tokens-remaining: 5000000
anthropic-ratelimit-input-tokens-reset: 2026-09-21T11:11:35Z
anthropic-ratelimit-output-tokens-limit: 1000000
anthropic-ratelimit-output-tokens-remaining: 1000000
anthropic-ratelimit-output-tokens-reset: 2026-09-21T11:11:36Z
x-new-api-version / x-oneapi-request-id          ← 中转软件指纹，两边都有
```

（上面的值来自多次真实抓取拼在一起，只为展示头的形状；同一次响应里 `request-id` 和各 ratelimit 值当然是同源的。）

#### ⚠️ `anthropic-ratelimit-*` 其实有两套，而且互斥

上面那 12 个是 **classic 家族**，只有**按量付费的 API key 组织**才会收到。特征是 `-limit` / `-remaining` / `-reset` 三件套、给的是**剩余条数**、reset 是 RFC 3339 时间串。

还有一套 **unified 家族**，只有**订阅登录（Claude Pro / Max）**才会收到。特征完全不同：给的是**已用比例**（`-utilization`），没有 `-limit`/`-remaining`，reset 是 **epoch 秒**，窗口是 **5 小时 + 7 天**（就是「你已达到本次会话限额 / 本周限额」那两个窗口）。实抓到的完整一套（13 个）：

```
anthropic-ratelimit-unified-status: allowed
anthropic-ratelimit-unified-reset: 1786641000
anthropic-ratelimit-unified-representative-claim: five_hour
anthropic-ratelimit-unified-5h-status: allowed
anthropic-ratelimit-unified-5h-utilization: 0.6
anthropic-ratelimit-unified-5h-reset: 1786641000
anthropic-ratelimit-unified-7d-status: allowed
anthropic-ratelimit-unified-7d-utilization: 0.32
anthropic-ratelimit-unified-7d-reset: 1786694400
anthropic-ratelimit-unified-fallback: available
anthropic-ratelimit-unified-fallback-percentage: 0.5
anthropic-ratelimit-unified-overage-status: rejected
anthropic-ratelimit-unified-overage-disabled-reason: org_level_disabled
```

**这是目前最硬的一个判据**：unified 家族出现在响应头里 = 上游是订阅号，不是 API key。它不是「像不像」的软信号，是 Anthropic 自己按认证方式分发的两套不同的头，伪造不了、也解释不掉。

两条现网实例，都在 `tob.goldlll.com`，同一天同一模型：

- 一条挂 `max无提示词` 标签的，头**没剥干净**，整套 unified 漏了出来 → 判 `subscription_max`（置信度 0.57）。
- 另一条挂 `官` 标签的，**同样整套 unified 漏了出来**，却判成了 `anthropic_api`（置信度 0.48）。**这条是漏判**——它根本不是官key，是订阅号。

所以 §1 的结论要补一句：**上游一旦忘了剥头，unified 家族比 `iterations[]` 更早、更硬地暴露 max 身份。**下面 max 样本统计里这一项是 0，只是因为那 40 条的中转都把 `anthropic-*` 剥光了——剥光是 max 渠道的常态，不是铁律。

全组统计：

| 头 | max（40 条） | 官key（16 条） |
|---|---|---|
| `anthropic-organization-id` | **0** | **16** |
| `anthropic-workspace-id` | 0 | 多数有 |
| `anthropic-ratelimit-*` classic（12 个） | **0** | 12 |
| `anthropic-ratelimit-unified-*`（13 个） | 0（都被剥了） | 0 |
| `request-id: req_...` | 2 / 40 | 16 / 16 |
| 裸 UUID 形 `x-request-id` | 多数 | 少数 |
| new-api 系指纹 | 31 / 40 | 16 / 16（零区分度） |

⚠️ **除 unified 家族外**，头都可以随便伪造，只能当弱证据；而且这层差异在流式下整个消失。
unified 家族是例外——它不是「有没有」的问题，是**结构本身**（比例 vs 剩余量、epoch vs RFC3339、5h/7d 窗口）只可能出自订阅认证。

### 2.3 响应体 —— 只差一个 `iterations[]`

max 侧：

```json
{"model":"claude-opus-5","id":"msg_011CfFtz…","type":"message","role":"assistant",
 "content":[{"type":"thinking","thinking":"","signature":"CAIStQIKpgEIEhgC…"}],
 "stop_reason":"max_tokens","stop_sequence":null,"stop_details":null,
 "usage":{"input_tokens":8,
          "cache_creation_input_tokens":0,"cache_read_input_tokens":0,
          "cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},
          "iterations":[{"input_tokens":8,"output_tokens":16,
                         "cache_read_input_tokens":0,"cache_creation_input_tokens":0,
                         "cache_creation":{…}}]}}
```

官key 侧：

```json
{"model":"claude-opus-4-7","id":"msg_011Cdzoi…","type":"message","role":"assistant",
 "content":[{"type":"text","text":"Hi there! How can I help you today?"}],
 "stop_reason":"end_turn","stop_sequence":null,"stop_details":null,
 "usage":{"input_tokens":13,
          "cache_creation_input_tokens":0,"cache_read_input_tokens":0,
          "cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},
          "output_tokens":16,
          "output_tokens_details":{"thinking_tokens":0},
          "service_tier":"standard",
          "inference_geo":"global"}}
```

字段级全库统计（max = 212 个带 usage 的响应 / 14 标签；官key = 126 个 / 6 标签）：

| `usage` 字段 | 官方规范 | max | 官key |
|---|---|---|---|
| `input_tokens` / `output_tokens` | ✅ | 212/212 | 126/126 |
| `cache_creation_input_tokens` / `cache_read_input_tokens` | ✅ | 212/212 | 126/126 |
| `cache_creation.{ephemeral_5m,1h}` | ✅ | 212/212 | 126/126 |
| `service_tier` | ✅ | **212/212** | 126/126 |
| `inference_geo` | ✅ | **212/212** | 108/126 |
| `output_tokens_details.thinking_tokens` | ✅ | **212/212** | 101/126 |
| `iterations[]` | ❌ **非官方字段** | **210/212** | 42/126 |

`iterations[]` 是中转软件把多轮内部调用拆开记账留下的痕迹，官方 Messages API 从不返回它（两轮 WebSearch + 官方文档核实）。

**注意 max 官方字段一个不缺**——早先"max 常缺 `service_tier`/`inference_geo`"的说法是错的，那是 DB 里 snippet 在 usage 中途被截断造成的假象。

---

## 3. 流式

### 3.1 响应头 —— 差异整个消失

现场 7 路抓包，**没有一路带 `anthropic-*` 头，包括官key 侧**。中转在 SSE 路径上普遍不转发上游头。

max 侧流式响应头：
```
server: openresty
content-type: text/event-stream
cache-control: no-cache
x-client-request-id: 6fc49661-…
x-request-id: e3e860e6-…
```

官key 侧流式响应头：
```
content-type: text/event-stream
cache-control: no-cache
alt-svc: h3=":443"; ma=2592000
via: 1.1 Caddy
x-accel-buffering: no
x-oneapi-request-id: 202609211047569234132608268d9d6bZxJiqgM
```

**流式下头层没有任何判据可用。**

### 3.2 SSE 原文对照

max 侧（`claude-opus-5`，逐字原文，只截断 signature 长串）：

```
event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-5","id":"msg_011CfGTCP3a9F1n2YEasuXHh","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":17,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":2,"service_tier":"standard","inference_geo":"not_available"}}     }

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}     }

event: ping
data: {"type":"ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"","estimated_tokens":null}          }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"CAISugIKpgEIEhgCKkA8lAqKgeNHsWogvCxyQI28…"}            }

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}      }

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hi there"}      }

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":", friend!"}    }

event: content_block_stop
data: {"type":"content_block_stop","index":1      }

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":17,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":27,"output_tokens_details":{"thinking_tokens":17},"iterations":[{"input_tokens":17,"output_tokens":27,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"message"}]},"context_management":{"applied_edits":[]}     }

event: message_stop
data: {"type":"message_stop"               }
```

官key 侧（`claude-opus-4-7`，逐字原文）：

```
event: message_start
data: {"type":"message_start","message":{"model":"claude-opus-4-7","id":"msg_011CfGTE60hqv3sN7wkcEJ4O","type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":22,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":1,"service_tier":"standard","inference_geo":"global"}}   }

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}   }

event: ping
data: {"type": "ping"}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi ther"}       }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"e"}       }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", "}        }

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"friend."}     }

event: content_block_stop
data: {"type":"content_block_stop","index":0   }

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":22,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":12,"output_tokens_details":{"thinking_tokens":0}},"context_management":{"applied_edits":[]}               }

event: message_stop
data: {"type":"message_stop"       }
```

**把这两段并排看，唯一的结构差异就是 `message_delta.usage` 里那个 `iterations[]`。**（上面 max 那段还多了个 `estimated_tokens`，但只有 1/7 渠道有，见 §5.3。）

### 3.3 七路样本矩阵

| 渠道 | 组 | `message_start.usage` | `message_delta.usage` | 事件序列 | `anthropic-*` 头 |
|---|---|---|---|---|---|
| cheapi-maxvip / opus-5 | max | +`service_tier` +`inference_geo` | +`cache_creation` **+`iterations`** +`output_tokens_details` | start→cbs→ping→cbd→cbstop→delta→stop | 无 |
| daodunapi-maxvip / opus-5 | max | 同上 | **有 iterations** | 同上 | 无 |
| 103.214.172.126-maxvip / opus-4-7 | max | 同上 | **有 iterations**，无 `cache_creation` | 同上 | 无 |
| 103.214.172.126:3000 / opus-4-7 | max | 同上 | 同上 | 同上 | 无 |
| 官key样本A / opus-4-7 | 官key | +`service_tier` +`inference_geo` | 无 iterations | 同上 | **无** |
| 官key样本B / opus-4-8 | 官key | +`service_tier`，无 `inference_geo` | 无 iterations | 无 ping | **无** |
| 官key样本C / opus-4-7 | 官key | 同上 | 无 iterations | 无 ping | **无** |

`iterations[]`：max 4/4 有，官key 0/3 有。**它在 `message_delta`，不在 `message_start`。**

---

## 4. 汇总

### 4.1 能区分的

| 层 | 信号 | 强度 |
|---|---|---|
| body | `usage.iterations[]` 存在（非流式 210/212 vs 42/126；流式 4/4 vs 0/3） | **强**，但见 §4.3 |
| 响应头（**仅非流式**） | `anthropic-ratelimit-unified-*` 出现 → 上游是订阅号（Pro/Max），**不可能是 API key** | **最强**，但只在中转忘了剥头时才有（2/519） |
| 响应头（**仅非流式**） | `anthropic-organization-id` / `anthropic-ratelimit-*` classic | 中，可伪造，流式下没有 |
| 行为 | `max_tokens` 被夹到 200 | 12/40 vs 0/16 |
| 行为 | 输入 token 多报（前缀注入 +13~+36 tok） | 2/40 vs 1/16 |
| 行为 | `input_tokens=0`（期望 ~7） | 5/40 vs 0/16 |
| 行为 | 伪造冷缓存 | 3/40 vs 0/16 |
| 行为 | kiro 计数注入 | 5/40 vs 0/16 |
| 行为 | 接受非默认采样 / 手动 thinking（真 max 本该拒） | 3、9 vs 0、2 |
| 错误文案 | 订阅制/额度原文（§5.4 的串） | 强，但只在出错时才有 |

### 4.2 不能区分的 —— 别再拿来当判据

- **官方 usage 字段的有无**：`service_tier` / `inference_geo` / `output_tokens_details` 两组都全。
- **`inference_geo` 的取值**：max 内部就有 `not_available` 和 `global` 两种。
- **流式响应头**：7 路样本全都没有 `anthropic-*`。
- **SSE 事件骨架 / `ping` 有无 / JSON 尾部空白填充量 / 换行数**：逐中转差异，跟 max/官key 无关。
  （顺带一提：尾部空白填充是 `api.anthropic.com` 的原生行为，填充为 0 说明中转把 body 重新序列化过——这能区分"透传 vs 重打包"，但**不能**区分 max 和官key，两者直连时都有填充。）
- **thinking signature 内嵌的 org UUID**：两组都有，见 §5.5。
- `cache_ttl_1h` 支持、`token-efficient-tools` beta gate、错误壳形状：三组全同。
- 请求头：见 §2.1。

### 4.3 一个必须记住的陷阱

渠道商的标签是营销词，不是技术事实。库里实证：

| 标签 | 实际响应头 | 实际 body |
|---|---|---|
| `tob.goldlll.com-官key` | **无任何 `anthropic-*`** | **有** `iterations[]` |
| `goldlll-max` | **有** `anthropic-organization-id` + `anthropic-ratelimit-*` | —— |

同一家渠道商，"官key"标签的反而剥了官方头、"max"标签的反而带官方头。

所以 **`iterations[]` 是中转软件标记，不是 max 专属标记**。我方 `max_no_prompt` 判定要求"剥头 **且** 有 iterations"两个条件同时成立（置信度地板 0.9），"不剥头但有 iterations"这种会被漏判成 `anthropic_api`。

---

## 5. 改造规格：把 max 出口改成官key 的样子

下面是给中转侧的实施清单。目标一句话：**下游客户收到的字节，看不出背后是 OAuth 订阅号还是官方 API key。**

分四层：响应头 → 响应体 → 错误文案 → 抹不平的。前三层都是纯出站改写，不动上游请求，不影响计费。

---

### 5.1 响应头：改成白名单，两边都剥光

**不要做的**：给 max 补 `anthropic-organization-id` / `anthropic-ratelimit-*`。理由在 §5.4，是硬理由。

**要做的**：出站响应头改**白名单**——只有名单内的头才转发给下游，其余一律丢掉，`x-request-id` 之类由中转自己生成。

放行名单（够用了）：

```
content-type
content-length / transfer-encoding
cache-control
anthropic-version        （上游回了就转）
x-should-retry
retry-after
```

必须拦掉的（**实测在漏**）：

| 头 | 来自 | 泄漏什么 |
|---|---|---|
| `x-client-request-id` | max 侧 8 个渠道 / 116 次 | **当前最强的"这是 max 渠道"指纹** |
| `anthropic-organization-id` | 官key 侧 | 上游账号的组织身份 |
| `anthropic-workspace-id` | 官key 侧 | 上游账号的 workspace |
| `anthropic-ratelimit-*` classic（12 个：requests / tokens / input-tokens / output-tokens 各配 limit / remaining / reset） | 官key 侧 | 上游账号的真实额度水位；负载均衡池后面这数字对客户毫无意义，客户照它退避只会被坑 |
| `anthropic-ratelimit-unified-*`（13 个：status / reset / representative-claim / 5h-status / 5h-utilization / 5h-reset / 7d-status / 7d-utilization / 7d-reset / fallback / fallback-percentage / overage-status / overage-disabled-reason） | max 侧漏 | **最致命的一个**——这套头只有订阅号才有，漏一个就等于自报「我背后是 Max 订阅」。剥头优先级排在其它所有头前面 |
| `x-upstream-request-id` | 官key 侧 | 明说还有上游 |
| `traceresponse` | 官key 侧 | 上游 trace id |
| `eo-cache-status` / `eo-log-uuid` | max 侧 30 次 | 腾讯 EdgeOne 指纹 |
| `l-request-id` / `l-via` / `l-version` | max 侧 10 次 | 某中转软件版本指纹 |
| `x-new-api-version` / `x-oneapi-request-id` | 两侧都有 | 中转软件身份（两边都有，所以不区分 max/官key，但它明说"我是中转"） |
| `pragma` / `expires` / `nel` / `report-to` | 两侧 | 中转 nginx / CDN 配置差异 |
| `server`（`openresty` / `nginx/1.28.3 (Ubuntu)`） | 两侧 | 中转软件+发行版版本 |

**为什么白名单而不是继续加黑名单**：这张"在漏"的表是从现网抓出来的，不是穷举——换个上游、换个 CDN 就多几个。黑名单永远追不上。

改完的额外好处：流式本来就没有 `anthropic-*` 头（§3.1），两边都剥光之后，**流式和非流式的头终于一致了**，不再有"非流式能查出来、流式查不出来"这种自相矛盾的签名。

---

### 5.2 响应体：删掉 `usage.iterations[]`

**这是 max 和官key 在 body 上唯一的差异**（§2.3、§3.2）。它是非官方字段，官方 Messages API 从不返回。

| | 改哪里 |
|---|---|
| 非流式 | 响应 JSON 的 `usage` 对象，删掉 `iterations` 这个键 |
| 流式 | **只有 `message_delta` 这一个事件**带它（`message_start` 不带）。解析该事件的 `data:` JSON，删掉 `usage.iterations`，重新序列化后再发给下游。 |

三个注意点：

1. **只删这一个键，别碰其它。** `service_tier` / `inference_geo` / `output_tokens_details` / `cache_creation` / `context_management` 全是官方字段，两组都有，删了反而露馅。
2. **别用正则替换，要 JSON 解析后重序列化。** `iterations` 数组里嵌套对象，正则切不干净。
3. **计费不受影响。** 计费只看 `input_tokens` / `output_tokens` / `cache_read_input_tokens` / `cache_creation_input_tokens`，`iterations` 只是重复记账的明细。删之前确认自己的计费逻辑没去读它。

顺带（可选）：部分 max 上游的 `content_block_delta` 里 `thinking_delta` 带 `estimated_tokens: null`，也是非官方字段，7 路样本里 1 路命中。既然已经在改 SSE，顺手 pop 掉；单为它动 SSE 不值。

---

### 5.3 错误文案：拦下会暴露身份的原文

出错时 body 里的原文最刺眼——直接写着"你的订阅额度用完了"。规则：

> **匹配即不透传**，统一 remap 成 `529 overloaded`（或 `503`）+ 换号重试，客户只看到"加载中→重试"。
> 匹配**看正文，不看状态码**——同一个原因，见过被包成 400、402、403、500 的。
> 例外：`HTTP 200` 一律不走这个判定（有独立的空响应守卫，混进来会跟计费打架）。

#### A. 订阅制限额 —— 命中即账号级侧移（5–10 分钟）+ 换号

小写包含匹配：

```
switch to another model          ← Claude Code 换模型提示，模型家族限额铁证
spend limit                      ← monthly / individual / org's / channel's spend limit
shared budget                    ← "You've hit your team's shared budget"
usage credits                    ← "Usage credits are required for long context requests."（实测 778 次）
limit reached                    ← 席位池 "limit reached, resets at <time>"
```

正则：

```
(?:reached|hit) your .{0,40}limit
```

covers `reached your Fable limit` / `hit your weekly limit` / `hit your Opus limit` / `hit your session limit`。

**所有格 `your X limit` 是刻意的**：上下文超长用的是 `token limit` / `context length`，不含 `your`，不会被误吃。

#### B. 第三方应用 / extra usage —— 命中即 60 秒短节流 + 换号（**不要**做账号级侧移）

```
extra usage
not your plan limits
claude.ai/settings/usage
with the claude pro plan
may not be enabled for your organization
restricted by your organization
all upstreams failed
```

实测原文：`"Third-party apps now draw from your extra usage, not your plan limits. Add more at claude.ai/settings/usage and keep going."`

**为什么这档不能跟 A 合并**：报这个 400 的同一小时里，同一个 key 仍有 706/832 请求成功——一个 key 背后挂 N 个真订阅号，只有部分号的 extra usage 归零。按账号级侧移会白扔一个 85% 健康的 key。当瞬时容量问题处理。

注意 `claude.ai/settings/usage` 这条：常见的 URL 脱敏只删带 `http://` / `https://` 的，**裸域名会逃过**，必须单独列。

#### C. 余额/额度耗尽

```
usage limit              wholesale credit         add credits
add additional credits   top up                   topup
out of credit            credit balance is too low    ← Anthropic 原生欠费文案
purchase credits         quota exceeded           payment required
operation not allowed    ← 中转对账号级拒绝的统一文案，error.type 为 null
余额    欠费    充值    额度不足    预扣费    剩余额度
```

`HTTP 402` 无条件命中，不用看正文。

`insufficient` 和 `billing` 这两个词**只在非 400 时匹配**——400 下太容易误吃请求格式错误（`insufficient tokens in prompt`）。

`预扣费` / `剩余额度` 必须显式列：实测 `"预扣费额度失败, 用户剩余额度: ＄0.032388, 需要预扣费额度: ＄1.460366"` 把上游账号的美元余额报给了客户，它现在能被拦住纯属侥幸——`剩余额度` 里恰好含子串 `余额`，句式一改就漏。

#### D. 中转自身的拦截/调度文案 —— 最容易被忽略的一档

这几条不是上游返的，是**中转自己**生成的，等于对客户宣告"你前面有个中间人"：

```
stopped locally before forwarding     ← 实测 400，194 次（2026-09-21 单日）
refusal guard                         ← 同一条的第二锚点，防句式改写
no available channel for model        ← 503，泄漏分组名 + distributor 身份
无可用渠道                             ← 同上的中文版
```

第一条的完整原文长这样：

> `Request blocked by the refusal guard. Anthropic's API previously refused this pattern (stop_reason="refusal", stop_details.category="reasoning_extraction"); stopped locally before forwarding. Anthropic classifies this as hidden-reasoning extraction or distillation, prohibited by its Usage Policy. Evidence: 1. dynamic_rule:7899896883446021: ...`

上游根本没收到这个请求——这是中转本地守卫拦的假 400，还附带内部规则号。换个号大概率能过，没理由透传。

`no available channel for model X under group vip (distributor)` 属容量类，按换号处理。

#### E. 限流被压成 400

```
upstream rate limit exceeded
正则: exceed(?:ed|s)? your (?:organization|account|org)'?s? rate limit
```

**这档只在 400 时匹配。** 上游返 429 时原样透传才是正确语义，客户需要知道要退避；把 429 remap 成 529 会破坏客户端的退避逻辑。

但 429 的**文案**要改写（下面 F）。

#### F. 不拦、只改文案

这几条语义上该透传，问题只在字面泄漏中转链。别进拦截表，在出站时把 message 换成通用文案即可：

| 原文 | 实测 | 泄漏什么 |
|---|---|---|
| `Upstream rate limit exceeded, please retry later` | 429，35 次 | `Upstream` 字样＝明说还有一层 |
| `Too many pending requests, please retry later` | 429，25 次 | 中转排队文案，官方 API 没这句 |
| `您已达到总请求数限制：N 分钟内最多请求 N 次，包括失败次数…` | 429，个位数 | 中文 + "包括失败次数"的计数口径，纯中转文案 |
| `<html>…<center>nginx</center>…</html>` | 502 | 裸 nginx 错误页 |

#### G. 反向名单 —— 这些**绝不能**拦，必须原样透传

优先级最高，命中即判"客户端错误"，一票否决上面 A–E 所有判定：

```
input is too long        too many tokens        token limit
context length           maximum context        exceeds the model
content length exceeds   prompt is too long     request too large
payload too large        max_tokens
```

拦了会出大事：Claude Code 靠这类文案触发**自动上下文压缩**，吞掉它客户端会卡死在无限重试。

同理这些请求格式错误也必须透传（客户端要靠它们修请求）：`Invalid signature in thinking block`、`input_schema does not support oneOf/allOf/anyOf`、`tool_result` 找不到对应 `tool_use`、图片尺寸超限等。

#### 验收方法

拿上面每一档的原文各造一条，走一遍中转，断言：

- A–E 的原文**一条都不出现在下游 body 里**，下游收到的是统一的 529/503；
- F 的原文被换成通用文案，但状态码仍是 429；
- G 的原文**逐字原样**出现在下游 body 里，状态码仍是 400。

第三条最容易写漏，也最容易出线上事故。

---

### 5.4 为什么不能靠伪造头蒙混过去

技术上能给 max 合成一套 `anthropic-organization-id` + `anthropic-ratelimit-*`。**不要做**，三个理由，第一个是硬的：

**1. 会跟 thinking signature 对不上。** `thinking` 块的 `signature` 是 Anthropic 签发的 blob，base64 解开后**明文**带着模型名和组织 UUID：

```
claude-opus-58
thinkingZ$70530798-14c0-4fcd-bc3c-651be8870c9f
```

max 侧和官key 侧都这样（所以它本身不是判据）。但只要走 thinking，真实组织 UUID 就一定会到客户手上——**这东西改不掉**，动了 signature 会让多轮 thinking 在下一轮被上游拒。客户拿 signature 里的真 UUID 跟你伪造的头一比，发现不是同一个，你等于**亲手造了一个原本不存在的破绽**。

**2. 数字是编的。** 负载均衡池后面 `ratelimit` 剩余额度没有真值。客户按一个编造的数字做退避决策，坑的是客户。

**3. 流式下两边本来都没这些头**（§3.1）。伪造只会造出一个新的"流式/非流式不一致"。

**正确方向是两边都剥光，不是两边都补齐。**

---

### 5.5 抹不平的：行为层

以下是上游的真实行为，**任何出站改写都解决不了**。发现了只能换渠道：

| 行为 | 命中率（max 组 / 官key 组） | 后果 |
|---|---|---|
| `max_tokens` 被夹到 200 | 12/40 vs 0/16 | 客户要长输出直接被截断 |
| 输入 token 多报 +13~+36（前缀注入） | 2/40 vs 1/16 | 按上游 usage 计费 → 客户多付 |
| `input_tokens=0`（期望 ~7） | 5/40 vs 0/16 | 计费失真 / 触发空响应守卫 |
| 伪造冷缓存 | 3/40 vs 0/16 | 缓存命中率和计费双失真 |
| 接受本该被拒的非默认采样 / 手动 `thinking.budget_tokens` | 3、9 vs 0、2 | 说明背后不是真 max 通道，行为不可预期 |

最后一条可以直接当探针用：官方 Opus 5 / 4.8 / 4.7 对 `thinking.budget_tokens` 必回 400，对 `temperature` 也回 400（已弃用）。**返 200 的，说明中转改写了你的请求。**

---

### 5.6 实施顺序

0. **先确认 `anthropic-ratelimit-unified-*` 一个都没往下游走**（§2.2）：一行判断的事，但它是唯一一个「漏一个就全盘暴露」的头族。做 §5.1 白名单会顺手覆盖，但别等白名单——先单独确认一遍。
1. **然后 §5.3 D 档**（中转自身文案）：改动最小，止血最快——现网单日 194 条 `stopped locally before forwarding` 正在漏给客户。
2. **再做 §5.1**（头改白名单）：纯出站，不碰流式字节，顺手把身份泄漏和中转指纹一起堵上。
3. **接着 §5.2 非流式**（删 `usage.iterations`）：一个键。
4. **最后 §5.2 流式**（改写 `message_delta`）：单独上线、单独观察。SSE 改写风险最高，别跟前面几步一起发。
5. **§5.5 行为层**另开工单，跟格式归一解耦。

做完 0–4，格式层就没有能区分 max 和官key 的信号了。剩下的只有 §5.5 的行为层和 signature 里那个改不掉的组织 UUID。


## 6. 取证边界

- §2、§4 的统计来自库内已有记录（`source_test_records`，519 条 `status='done'`），未发请求。
- §3 的流式 SSE 是 2026-09-21 现场实抓（7 次调用，`max_tokens=64`），原始件在 job 临时目录，未入仓库，key 未落盘。
- §5.4 的 signature 解码来自同批抓包。
- §2.2 的 `anthropic-ratelimit-unified-*` 完整头族取自库内两条真实记录（`tob.goldlll.com`，2026-08-04 / 08-13），「unified = 订阅登录、classic = API key」这条分野已对 Anthropic 官方 Rate limits 文档及 claude-code issue 交叉核对。
- 库里**没有 `api.anthropic.com` 直连基线**，所有"官key"都是中转链路，官方侧结论受此限制。
- §5 是给中转侧的实施规格，不涉及本地改动。
