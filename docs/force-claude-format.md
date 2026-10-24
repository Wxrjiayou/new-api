# 强制 Claude 格式化（Force Claude Format）

## 功能概述

渠道级开关，开启后将 Claude Max 订阅渠道的响应归一化为官方 Anthropic API Key 格式，使下游客户端无法从响应格式层面区分 Max 订阅和官方 API Key。

仅作用于 Claude 原生格式路径（`/v1/messages`），不影响 OpenAI 兼容格式（`/v1/chat/completions`）。

## 开启方式

渠道管理 → 创建/编辑渠道（Anthropic 类型） → 高级设置 → 渠道额外设置 → **强制 Claude 格式化** 开关。

## 归一化范围

### 响应体

| 操作 | 字段 | 说明 |
|---|---|---|
| 删除 | `usage.iterations` | Max 独有的迭代计费明细 |
| 删除 | `context_management` | Max 独有的上下文管理信息 |
| 删除 | 流式 `message_delta` 中的 `usage.cache_creation` | Max 在 delta 事件中额外携带的缓存创建对象，官方 delta 不包含 |
| 添加 | `container: null` | 官方响应固有字段，Max 不返回 |
| 改值 | `usage.inference_geo`: `"not_available"` → `"global"` | Max 返回 `not_available`，官方返回实际地理标识 |

以上操作同时覆盖非流式响应和流式 SSE 的 `message_start`、`message_delta` 事件，其余事件类型（`content_block_start`、`content_block_delta`、`content_block_stop`、`ping`、`message_stop`）原样透传。

### 响应头（非流式）

| 类别 | 内容 |
|---|---|
| 白名单过滤 | 仅透传 `content-type`、`cache-control`、`anthropic-version`、`x-should-retry`、`retry-after`，丢弃上游其余头 |
| 合成身份头 | `request-id`（Base58 格式）、`anthropic-organization-id`、`anthropic-workspace-id`（基于下游 API Key 稳定派生） |
| 安全头 | `content-security-policy`、`strict-transport-security`、`x-robots-tag` |
| 限流状态头 | 12 个 `anthropic-ratelimit-*` 头（requests / tokens / input-tokens / output-tokens 各含 limit / remaining / reset） |

### 响应头（流式）

流式下仅保留标准 SSE 头（`content-type`、`cache-control`、`transfer-encoding`、`x-accel-buffering`），不注入 `anthropic-*` 头，与官方流式行为一致。同时屏蔽 Codex 相关头（`X-Reasoning-Included`、`X-Codex-Turn-State`）防止泄漏。

## 限流状态机

基于下游 API Key 维护进程内滑动窗口计数器（分钟对齐），跟踪四个维度：请求数、总 token、输入 token、输出 token。非流式响应头中的 `remaining` 和 `reset` 值由此计数器驱动。默认上限：RPM 1000 / TPM 400,000 / 输入 TPM 320,000 / 输出 TPM 80,000。

## 已知局限

- **中间件头泄漏**：`X-Oneapi-Request-Id` 及 nginx 安全头（`X-Frame-Options` 等）由中间件/反向代理层在归一化之后写入，当前未处理。
- **Workspace-Id 格式**：合成值为 UUID 格式，官方为 `wrkspc_` 前缀格式。
- **限流数值**：使用固定默认值，未关联下游用户实际配额。

## 不涉及

- 请求体修改
- 错误响应文案拦截
- OpenAI 兼容格式路径
