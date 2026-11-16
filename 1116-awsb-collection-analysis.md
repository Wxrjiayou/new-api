# 1116 AWSB 全量采集测试报告（ADM vs XDM vs MAX 原始）

测试时间: 2026-11-16
脚本: `tests/awsb_cases/collect.py`（98 用例，只采集不判断，用例定义与官方预期注释见 `tests/awsb_cases/cases.py`）
模型: `claude-sonnet-4-6`

## 测试对象与原始数据位置

| 渠道 | 说明 | 原始数据目录 |
|------|------|-------------|
| ADM | 我们的实例，ForceBedrockFormat=true | `tests/awsb_cases/results/adm_20261116_080409/` |
| XDM | 真实 AWSB（xdmzhan，有 system 注入） | `tests/awsb_cases/results/xdm_20261116_080444/` |
| MAX | cheapi 订阅号池原始渠道（ADM 的上游，未归一化） | `tests/awsb_cases/results/max_20261116_081442/` |

每个用例目录含: request.json（key 脱敏）、response_headers.json、response_body.json / stream_raw.sse + stream_events.txt、meta.json（含官方预期注释）。汇总见各目录下 `index.csv`。

## 总览

- 98 用例双方均无网络错误
- **87 例 HTTP 状态一致，11 例不匹配**
- 二步用例（缓存重发/工具回传/thinking 回传）中 thinking 回传第二步失败

状态不匹配清单: `thinking_roundtrip(step2)`, `tools_server_web_fetch`, `tools_computer_wrong_version`, `mm_image_bad_media`, `err_no_max_tokens`, `err_max_tokens_zero`, `err_max_tokens_zero_stream`, `err_max_tokens_over_limit`, `err_bad_model`, `beta_invalid`, `beta_compaction`

---

## 一、证实的遗留待办（1112/1113 已知，全部实锤）

### 1. thinking 签名回传失败（P0）

`thinking_roundtrip` 第二步：ADM 400，XDM 200 正常续答。

ADM 错误（剥掉双重包装后的内层）:
```
messages.1.content.0: Invalid `signature` in `thinking` block
```

结论: 剥离 protobuf 字段（field 14/17/22）后的签名回传 MAX 校验必失败 → **多轮 thinking 对话在 ADM 上不可用**。1112 文档 #2 的"多轮对话上行还原"必须做（缓存原签名映射还原，或放弃剥离）。

### 2. 双重 Bedrock 包装

所有上游错误场景（bad_role / prefill / web_fetch / 签名回传 / over_limit / beta_invalid）ADM 消息均嵌套两层前缀:

```
operation error Bedrock Runtime: InvokeModel, ... ValidationException:
  operation error Bedrock Runtime: InvokeModel, ... ValidationException: <真实消息>
```

内层 RequestID 已被脱敏为 `***`，但嵌套结构本身是指纹。

**根因修正（MAX 原始数据证实）**：MAX 原始错误是纯 Claude 格式（`invalid_request_error` + 原始消息），**没有任何 Bedrock 前缀**。1113 文档"上游 cheapi 也是 new-api 已做 Bedrock 包装"的假设不成立——双重包装是**我们自己的管线包了两次**（`857277edd` 在 claude_handler/relay-claude 加的 `WrapBedrockRequestError` 与 `7ad8a04f4` 在 `NormalizeUpstreamError` 末尾的 `wrapBedrockErrorFormat` 对同一错误先后生效）。

修复: 包装入口检测已含 `operation error Bedrock Runtime` 前缀则跳过，或去掉其中一个包装点。

### 3. A 类 max_tokens=0（含流式）

ADM 400（`valid_request.go` 拦截）/ XDM 200（返回 1 token，stop_reason=max_tokens）。

---

## 二、新发现

### 4. `usage.speed` 泄漏（新 P0）

ADM 所有响应 `usage.speed = "standard"`，XDM 非流式恒为 `null`、**流式 message_start/message_delta 根本没有 speed 键**。MAX 原始数据证实上游确实返回 `speed:"standard"`（1113 raw 对比文档"speed 全场景 null"的结论已过时），`!Exists()` 保护保留了真实值 → 每个响应都带此指纹。

修复: Bedrock 模式下非流式强制 `usage.speed = null`，流式直接删除 speed 键。

### 4b. `safeguard_results` 泄漏（新 P0，MAX 原始数据发现）

MAX 原始响应根级带 `safeguard_results: []`（新出现的 MAX 专有字段，1112/1113 文档均未记录），ADM **原样透传**，XDM 无此字段。所有非流式响应均带。

修复: `normalizeClaudeBody` / `normalizeClaudeStreamEvent` 增加删除 `safeguard_results`（流式 message_start 是否携带待查 stream_raw.sse）。

### 5. 1h 缓存跨请求不命中（P2 实锤）

| 用例 | 第一次 | 第二次 |
|------|--------|--------|
| cache_create_read (5m) ADM | creation=8173, read=0 | creation=0, **read=8173** ✓ |
| cache_create_read (5m) XDM | creation=8173, read=0 | creation=0, read=8173 ✓ |
| cache_ttl_1h ADM | creation=8172 (1h) | **creation=8172 (1h)，未命中** ✗ |
| cache_ttl_1h XDM | creation=8172 (1h) | creation=0, read=8172 ✓ |

5m 缓存两边行为与数值完全一致；1h TTL 在 ADM 上第二次仍重新写缓存。

**根因修正（MAX 原始数据证实）**：MAX 直连的 1h 缓存两次请求**都命中读**（cache_read=8172，甚至第一次就命中了 ADM 早前写入的缓存——说明 ADM 与 MAX 直连共享上游账号，且上游 1h TTL 完全正常）。ADM 第二次未命中不是 MAX 不支持 1h，而是**两次请求被路由到了不同上游渠道/账号**。对应 1112 文档 #7"亲和命中（未做）"——需要缓存亲和路由（同一缓存前缀的请求固定到同一上游渠道）。

### 6. 缺少 max_tokens: XDM 200 / ADM 400

XDM 网关补默认值正常回答（output_tokens=13, end_turn）；ADM 被 `7cbbc7fa6`（require max_tokens）拦截。与参考实现冲突，A 类扩展。

### 7. max_tokens 超上限: XDM 200 / ADM 400

`max_tokens=999999`: XDM 截断到模型上限正常回答；ADM 400。A 类扩展。

### 8. image/bmp: ADM 200 / XDM 400（请求侧欠拦截）

MAX 接受非法 media_type `image/bmp`；真 Bedrock 拒绝:
```
ValidationException: Image media_type must be JPEG, PNG, GIF, or WebP
```
修复: Bedrock 模式请求侧加 media_type 白名单（jpeg/png/gif/webp）。

### 9. web_fetch: ADM 400 / XDM 200（过度拒绝）

真 AWSB 接受 `web_fetch_20250910` 工具声明（不执行 fetch，正常回文本）；ADM 的 `unsupportedServerTools` 列表拒绝它。web_search / code_execution 两边都 400，应保留。

修复: 从拒绝列表移除 `web_fetch_20250910`。

### 10. error.type 系统性差异

| 项 | ADM | XDM |
|----|-----|-----|
| error.type | `invalid_request_error` | `<nil>` |
| 消息前缀 | 无 | `InvokeModel: ` |
| 消息后缀 | 无 | `(request id: ...)`（new-api 中转后缀） |

即 1113 错误分析的方案 2（type 对齐）未实施部分。可选修复。

### 11. beta 头行为相反 ×2（上游差异，记录）

- `beta_invalid`（未知 beta 值）: XDM 忽略返回 200；ADM 400（上游 MAX 拒绝）
- `beta_compaction`: ADM 200（MAX 支持 compact_20260112）；XDM 400（其 Bedrock 路径不认此 tag，版本较老）

### 12. computer 旧版本: XDM 200 / ADM 400（上游差异，难修）

Bedrock 接受 `computer_20241022`（返回 tool_use）；MAX 拒绝。网关无法凭空伪造 tool_use，记录即可。

### 13. err_bad_model: ADM 503 / XDM 404（已知 B 类，不修）

---

## 三、验证通过项

- **cache_creation 真实值精确保留**（8173/8172，`786a95c5b` keepCacheCreation 修复有效，5m 场景）
- **toolu_bdrk_ id 上行还原正常**（tools_roundtrip / tools_roundtrip_error 两步全 200）
- invocationMetrics 真实值正常（firstByteLatency/invocationLatency/token 数均非零）
- `stop_details: null`、`msg_bdrk_`/`toolu_bdrk_` 前缀、usage 字段结构全对齐
- prefill 两边都 400（Bedrock 4.6 也拒: "This model does not support assistant message prefill"，ADM 侧该消息被双重包装遮住）
- top_k=501 / -1 两边都 200（Bedrock 实际不强制文档的 0..500 范围，无需拦截）
- temperature=1.5 / 2.0、top_p=2.0、temp+top_p 同设: 两边都 400 ✓
- refusal 探针两边都未触发（end_turn，无 stop_details）
- unsup 探针（inference_geo / service_tier=priority / speed=fast / fallbacks）两边都 200 —— **XDM 也透传未知参数，这些不是判别点**；真正的判别点是 #4 的 usage.speed 回显
- 缺失/非法 anthropic-version 头: 两边都 200（网关不校验）
- count_tokens: 两边都 404（XDM 也未实现此端点）
- thinking 签名长度: ADM 564 vs XDM 860 字符（已知签名指纹差异，1112 #2）

---

## 四、修复优先级

| 优先级 | 项 | 位置 |
|--------|-----|------|
| P0 | 签名回传还原（缓存映射或不剥离） | claude_normalize.go / claude_normalize_request.go |
| P0 | 双重包装（我们自己的两个包装点先后生效） | WrapBedrockRequestError / wrapBedrockErrorFormat 去重 |
| P0 | usage.speed: 非流式置 null / 流式删键 | bedrockOverlayBody / bedrockOverlayStreamEvent |
| P0 | safeguard_results 剥离 | normalizeClaudeBody / normalizeClaudeStreamEvent |
| P1 | max_tokens 对齐 XDM: 缺失补默认 / 0→1 / 超限 clamp 64K（**适配器改写，不能只删校验**） | valid_request.go + adaptor.go（**涉计费，动手前须读 .agents/rules/billing.md**） |
| P1 | media_type 白名单（jpeg/png/gif/webp） | claude_normalize_request.go |
| P1 | web_fetch 移出拒绝列表 | unsupportedServerTools |
| P1 | 缓存亲和路由（同前缀固定同渠道，解决 1h 缓存不命中） | 渠道选择层（1112 #7） |
| P2 | error.type 对齐（<nil> + InvokeModel: 前缀） | 1113 方案 2 |
| P2 | 未知 beta 头剥离（对齐 XDM 的忽略行为） | 请求头处理 |
| 不修 | bad_model 状态码、compaction 差异、computer 旧版本 | 上游/网关配置差异 |

---

## 五、cheapi（MAX 原始渠道）三方对照

MAX 直连 98 用例全部完成，无网络错误（状态分布: 200×60, 400×34, 404×1, 401×2, 502×1）。三方状态不一致共 16 例。

### 5.1 MAX 原始数据带来的根因修正

| 项 | 修正 |
|----|------|
| 双重包装 | MAX 原始错误无 Bedrock 前缀 → 双重包装来自我们自己的两个包装点（见一.2） |
| 1h 缓存 | MAX 直连两次都命中读 → 上游 1h TTL 正常，ADM 未命中是渠道/账号轮换（见二.5，亲和路由） |
| thinking 签名 | MAX 原始签名 596 字符（ADM 剥离后 564），MAX 直连回传第二步 200 正常 → 回传失败纯由剥离引起 |
| safeguard_results | MAX 新专有字段，ADM 透传泄漏（见二.4b） |

### 5.2 服务端工具三方行为

| 工具 | MAX 原始 | ADM | XDM | 结论 |
|------|---------|-----|-----|------|
| web_search | **200 真执行**（server_tool_use + web_search_tool_result 块） | 400 | 400 | ADM 拒绝正确 ✓ |
| code_execution | 200（未执行，纯 text 回答） | 400 | 400 | ADM 拒绝正确 ✓ |
| web_fetch | 200 | 400 | **200**（接受声明不执行） | ADM 过度拒绝，应放行 |

### 5.3 max_tokens 系列: 修复方案需要注意

MAX 原始对 0/缺失/超限**全部 400**：
- `max_tokens=0`: "stream cannot be true when max_tokens is 0"（MAX 非流式其实支持 0 做缓存预热，cheapi 上游强制流式所以拒）
- 缺失: "max_tokens: Field required"
- 超限: "999999 > **128000**"（注意 MAX 侧 Sonnet 4.6 上限 128K，Bedrock 模型卡是 64K）

而 XDM 全部 200（缺失补默认、超限截断、0 返回 1 token）。**因此对齐 XDM 不能只删我们的校验**——删了校验 MAX 上游还是会 400。需要在适配器层做请求改写：缺失→补默认值；0→改写为 1（模拟 Bedrock 返回 1 token）；超限→clamp 到 64K。

### 5.4 unsup 探针: 不是判别点（三方证据齐全）

inference_geo / service_tier=priority / speed=fast：MAX 原始 400（明确拒绝），ADM 与 XDM 都 200——两个网关都在 DTO 层丢弃了这些未知字段再转发。**与参考实现行为一致，无需处理**。唯一残留判别是响应侧 usage.speed 回显（二.4）。

### 5.5 其余三方差异

| 用例 | MAX | ADM | XDM | 说明 |
|------|-----|-----|-----|------|
| err_bad_model | 404 | 503 | 404 | ADM 的 503 是我们网关渠道选择行为（B 类） |
| beta_invalid | 400 | 400 | 200 | XDM 丢弃未知 beta 头；要对齐需网关剥头而非透传 |
| beta_compaction | 200 | 200 | 400 | XDM Bedrock 路径较老不认 compact tag；我们"比真 AWSB 更能"，记录 |
| tools_computer_wrong_version | 400 | 400 | 200 | 上游差异，难修 |
| mm_image_bad_media | 200 | 200 | 400 | 需请求侧 media_type 白名单（二.8） |
| ep_count_tokens | 502 | 404 | 404 | cheapi 未实现；ADM/XDM 一致 |

### 5.6 MAX 原始字段清单（归一化层的完整剥离对照）

MAX 根级字段: `container, content, diagnostics, id, model, role, safeguard_results, stop_details, stop_reason, stop_sequence, type, usage`

ADM 已剥离: container ✓, diagnostics ✓；**遗漏: safeguard_results ✗**
MAX usage: `speed:"standard"`, `inference_geo:"global"`（ADM 已覆盖 inference_geo ✓，speed 遗漏 ✗）

---

## 六、三方逐项差异总表（98 用例）

状态列 `200→400` 表示二步用例的第一步→第二步。

### 6.1 系统性字段差异（影响所有响应，与单用例无关）

| 项 | MAX 原始 | ADM | XDM 真实 AWSB | 修改建议 |
|----|---------|-----|--------------|----------|
| usage.speed（非流式） | "standard" | "standard" ✗ | null | P0 强制置 null |
| usage.speed（流式） | "standard" | "standard" ✗ | 无此键 | P0 删除键 |
| safeguard_results（根级） | [] | [] ✗ | 无此字段 | P0 剥离 |
| usage.inference_geo | "global" | "not_available" ✓ | "not_available" | 已对齐 ✓ |
| container / diagnostics | 有 | 已删 ✓ | 无 | 已对齐 ✓ |
| thinking signature 长度 | 596 | 564（剥离后） | 860 | P0 签名策略重做 |
| 错误外壳 error.type | invalid_request_error | invalid_request_error ✗ | "<nil>" | P2 对齐 |
| 错误消息前缀 | 无 | 双重 Bedrock 包装 ✗ | 单层 + "InvokeModel: " 前缀 | P0 去重 / P2 前缀 |
| 错误消息后缀 | 无 | 无 | "(request id: ...)"（new-api 共性） | 不处理 |
| ID 前缀 | msg_01 / toolu_01 | msg_bdrk_ / toolu_bdrk_ ✓ | msg_bdrk_ / toolu_bdrk_ | 已对齐 ✓ |
| invocationMetrics | 无 | 真实值 ✓ | 真实值 | 已对齐 ✓ |

### 6.2 逐用例状态与差异

| 用例 | MAX | ADM | XDM | 差异说明 | 修改建议 |
|------|-----|-----|-----|----------|----------|
| basic_minimal | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_minimal_stream | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_content_blocks | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_system_string | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_system_blocks | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_multi_turn | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_consecutive_user | 200 | 200 | 200 | 三方都 200，Bedrock 实际也合并同 role 消息 | 无需处理 |
| basic_assistant_prefill | 400 | 400 | 400 | 三方都 400（Bedrock 4.6 也拒 prefill），但 ADM 消息被双重包装遮住 | P0 双重包装去重 |
| basic_truncate | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_stop_seq_hit | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_stop_seq_miss | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| basic_long_output_stream | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_temp_0 | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_temp_05 | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_temp_1 | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_temp_1_5 | 400 | 400 | 400 | 三方都 400，判别点通过 | 仅错误外壳问题（见系统性差异） |
| sampling_temp_2 | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| sampling_temp_negative | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| sampling_top_p_01 | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_top_p_2 | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| sampling_top_k_500 | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| sampling_top_k_501 | 200 | 200 | 200 | 三方都 200，Bedrock 实际不强制文档 0..500 范围 | 无需拦截 |
| sampling_top_k_negative | 200 | 200 | 200 | 三方都 200，同上 | 无需拦截 |
| sampling_temp_and_top_p | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| sampling_temp_string_type | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| thinking_budget_min | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| thinking_budget_1023 | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| thinking_budget_ge_max | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| thinking_nonstream | 200 | 200 | 200 | 签名长度 MAX 596 / ADM 564 / XDM 860 字符 | P0 签名策略（配合回传修复） |
| thinking_stream | 200 | 200 | 200 | signature_delta 原文同上差异 | 同上 |
| thinking_adaptive | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| thinking_invalid_type | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| thinking_with_temperature | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| thinking_roundtrip | 200→200 | 200→400 | 200→200 | 第二步 MAX 200 / ADM 400 / XDM 200：剥离后签名回传 MAX 校验失败 | P0 签名缓存映射还原或不剥离 |
| tools_nonstream | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_stream | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_choice_auto | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_choice_any | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_choice_tool | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_choice_none | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_choice_nonexistent | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| tools_disable_parallel | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_parallel | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_roundtrip | 200→200 | 200→200 | 200→200 | 第二步三方 200，bdrk id 上行还原正常 | 无需处理 ✓ |
| tools_roundtrip_error | 200→200 | 200→200 | 200→200 | 第二步三方 200，is_error 回传正常 | 无需处理 ✓ |
| tools_invalid_schema | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| tools_server_web_search | 200 | 400 | 400 | MAX 200 真执行搜索 / ADM 400 / XDM 400 | ADM 拒绝正确，保留 ✓ |
| tools_server_web_fetch | 200 | 400 | 200 | MAX 200 / ADM 400 / XDM 200（接受声明不执行） | P1 从拒绝列表移除 web_fetch |
| tools_server_code_exec | 200 | 400 | 400 | MAX 200 / ADM 400 / XDM 400 | ADM 拒绝正确，保留 ✓ |
| tools_bash | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_text_editor | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_computer_correct | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| tools_computer_wrong_version | 400 | 400 | 200 | MAX 400 / ADM 400 / XDM 200（Bedrock 接受旧版本） | 上游差异，难修，记录 |
| mm_image_png | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| mm_image_url | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| mm_image_bad_b64 | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| mm_image_bad_media | 200 | 200 | 400 | MAX 200（接受 bmp）/ ADM 200 / XDM 400 | P1 请求侧 media_type 白名单 |
| mm_pdf_document | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| mm_citations | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| cache_create_read | 200→200 | 200→200 | 200→200 | 两步数值完全一致（8173 写入→命中） | keepCacheCreation 修复有效 ✓ |
| cache_ttl_1h | 200→200 | 200→200 | 200→200 | 第二步 MAX 命中读 / ADM 重新写 8172 / XDM 命中读 | P1 缓存亲和路由（同前缀固定同渠道） |
| cache_toplevel_auto | 200 | 200 | 200 | 三方都 200（文档说 Bedrock 拒，XDM 实际放行） | 与参考一致，无需处理 |
| cache_in_messages | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| cache_in_tools | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| cache_short_prefix | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| err_no_max_tokens | 400 | 400 | 200 | MAX 400 / ADM 400 / XDM 200（网关补默认值） | P1 适配器补默认（只删校验没用，MAX 会拒） |
| err_max_tokens_zero | 400 | 400 | 200 | MAX 400 / ADM 400 / XDM 200（返回 1 token） | P1 适配器 0→1 改写 |
| err_max_tokens_zero_stream | 400 | 400 | 200 | 同上，流式路径 | P1 同上 |
| err_max_tokens_negative | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_max_tokens_over_limit | 400 | 400 | 200 | MAX 400（上限 128K）/ ADM 400 / XDM 200（截断） | P1 适配器 clamp 到 64K |
| err_no_model | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_bad_model | 404 | 503 | 404 | MAX 404 / ADM 503 / XDM 404 | B 类网关渠道选择差异，不修 |
| err_no_messages | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_empty_messages | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_bad_role | 400 | 400 | 400 | 三方 400；ADM 双重包装 + error.type 差异；XDM 消息带 InvokeModel: 前缀 | P0 去重 + P2 type/前缀对齐 |
| err_assistant_only | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_body_empty | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_body_not_json | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_body_truncated | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_content_bad_type | 400 | 400 | 400 | 三方状态一致；ADM 上游错误有双重包装/type 差异 | P0 去重（外壳见系统性差异） |
| err_key_invalid | 401 | 401 | 401 | 三方 401 | 一致 ✓ |
| err_key_missing | 401 | 401 | 401 | 状态一致（字段级见系统性差异） | — |
| err_no_version_header | 200 | 200 | 200 | 三方 200（网关都不校验此头） | 无需处理 |
| err_bad_version_header | 200 | 200 | 200 | 三方 200，同上 | 无需处理 |
| unsup_inference_geo | 400 | 200 | 200 | MAX 400 / ADM 200 / XDM 200（两网关都丢弃未知字段） | 与参考一致，无需处理 |
| unsup_service_tier_priority | 400 | 200 | 200 | 同上 | 无需处理 |
| unsup_speed_fast | 400 | 200 | 200 | 同上；但响应侧 usage.speed 回显是泄漏 | P0 speed 覆盖（见系统性差异） |
| unsup_fallbacks | 200 | 200 | 200 | 三方 200 | 无需处理 |
| probe_refusal | 200 | 200 | 200 | 三方 200 end_turn，均未触发 refusal | 未采集到 refusal 形态，可换更强探针 |
| beta_context_1m | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| beta_interleaved_thinking | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| beta_invalid | 400 | 400 | 200 | MAX 400 / ADM 400 / XDM 200（XDM 丢弃未知 beta 头） | P2 网关剥离未知 beta 头 |
| beta_effort | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| beta_context_management | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| beta_compaction | 200 | 200 | 400 | MAX 200 / ADM 200 / XDM 400（XDM Bedrock 路径较老） | 我们比参考更能，不修 |
| beta_structured_output | 200 | 200 | 200 | 状态一致（字段级见系统性差异） | — |
| ep_count_tokens | 502 | 404 | 404 | MAX 502 / ADM 404 / XDM 404 | ADM 与参考一致 ✓ |
| ep_models_list | 200 | 200 | 200 | 三方 200（都返回网关自己的模型列表） | 中转共性指纹，无法避免 |
