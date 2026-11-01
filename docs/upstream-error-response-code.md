# 上游错误响应码改写（feat/response-code）

本文记录 `feat/response-code` 分支对上游错误响应码和响应内容的改写，以及暂未实施的 403 改写。对照依据是 `docs/ltapi-upstream-non400-error-handling.md` 第 4 节「对客户端的错误改写」。

## 1. 背景

对中转站来说，客户端持有的是**我们的 key**。上游返回的 401、404、500 说的是**我们渠道**的问题（key 失效、模型映射错误、上游故障），客户端无法处理。原样透传会误导客户端，例如 Claude Code 收到 401 会提示用户重新 /login。

ltapi 的做法是把这类错误统一改写成 529 overloaded，并带 `Retry-After: 5`，让客户端稍后重试。本次修改在 new-api 中实现了其中的 401、404、500，以及透传错误的正文清洗。

## 2. 修改内容

所有规则都在 `service/upstream_error_normalize.go` 的 `NormalizeUpstreamError` 中实现。该函数在重试循环里每次尝试失败后调用一次（`controller/relay.go`），发生在是否重试的判断之前。

### 2.1 状态码改写

| 上游状态码 | 修改前客户端收到 | 修改后客户端收到 |
|---|---|---|
| 401 | 原样透传 401（只有正文命中 OAuth 等关键词时才改写） | Claude 格式请求：**529** `overloaded_error` "Overloaded"；其他格式：**503** "Overloaded"；都带 `Retry-After: 5` |
| 404 | 原样透传 404 | 同上 |
| 500 | 原样透传 500 | 同上 |

非 Claude 格式给 503 而不是 529，是因为 529 是 Anthropic 自己定义的状态码，OpenAI / Gemini 的 SDK 不一定认识；503 是所有官方 SDK 都会重试的标准状态码。这一点沿用了原有余额类错误的做法。

例外，以下情况不改写：

| 情况 | 处理 | 原因 |
|---|---|---|
| 正文命中白名单关键词（上下文超长、`max_tokens`、`invalid signature` 等） | 原样透传（正文经过清洗） | 这类是客户端请求本身的问题，客户端需要看到准确信息 |
| `/v1/tools/fetch` 返回的 404 | 原样透传（正文经过清洗） | 表示客户端要抓取的页面不存在，是客户端的问题 |
| 网关自己产生的错误（`ErrorTypeNewAPIError`，例如用户额度不足、连接上游失败） | 不经过本规则 | 不是上游错误 |

### 2.2 Retry-After

`NormalizeUpstreamError` 返回的**所有 Overloaded 错误**都会带 `Retry-After: 5`，包括：

- 本次新增的 401 / 404 / 500 改写；
- 原有的 429 / 503 改写为 "Overloaded"；
- 原有的余额类、订阅类、上游失效类关键词改写为 529 / 503；
- 原有的 400 + 上游限流关键词改写为 429。

实现方式：`NormalizeUpstreamError` 把返回的 Overloaded 错误记在请求上下文里（`upstreamOverloadedErrorKey`）。`controller/relay.go` 写最终错误响应时调用 `service.UpstreamErrorRetryAfter`，**只有最终返回给客户端的错误就是被记下的那个**时才写 `Retry-After` 响应头。所以：

- 失败后换渠道重试成功了，成功的响应不会带 `Retry-After`；
- 后面一次尝试返回了别的错误（比如选不到渠道），也不会带 `Retry-After`。

不带 `Retry-After` 的情况：

- 原样透传的错误；
- HTML 正文改写成的 "Internal server error"；
- 流式响应已经开始输出后的错误：响应头已经发出，改不了。

### 2.3 透传错误的正文清洗

原样透传的上游错误，状态码不变，正文里的上游标识会被清洗掉：

| 内容 | 示例 | 清洗后 |
|---|---|---|
| 下游 new-api 中转附加的 request id | `upstream timeout (request id: 2026110112345)` | `upstream timeout` |
| AWS ARN | `arn:aws:bedrock:us-east-1:123456789012:inference-profile/x` | `arn:***` |
| AWS 账号 ID（前面有 account 字样） | `account 123456789012` | `account ***` |
| request / trace / correlation id 的值 | `RequestID: 6f1c…`、`"trace_id":"abc123"` | `RequestID: ***`、`"trace_id":"***"` |
| Anthropic request id | `req_011CUa1b2c3d4e5f6g7h8i9j` | `req_***` |
| Azure 订阅、资源组路径 | `/subscriptions/<uuid>/resourceGroups/prod-rg` | `/subscriptions/***/resourceGroups/***` |
| GCP 项目路径 | `projects/my-gcp-project/locations/…` | `projects/***/locations/…` |
| Bearer token | `Bearer eyJhbGci…` | `Bearer ***` |
| `sk-` 开头的 key | `sk-proj-abcdefghijklmnop1234` | `sk-***` |

当前渠道的上游地址、IP、URL 查询参数，由原有的 `kitutil.MaskSensitiveInfo` 在写响应时遮住，本次未改动。

上游正文里的多余字段（`request_id`、`trace_id`、`innererror`、`debug_info`、`deployment_id` 等）不会带给客户端：错误响应是网关用 message / type / code / param 重新组装的，这些字段本来就会被丢掉。

### 2.4 不受影响的行为

| 项目 | 说明 |
|---|---|
| 重试 | 改写后的 529 / 503 和原来的 401 / 404 / 500 都在可重试范围内，换渠道重试的行为不变 |
| 自动禁用渠道 | 判断的是**原始错误**，上游 401 照样会禁用渠道 |
| 管理员查看原始错误 | 被改写或清洗过的错误，原始状态码、类型、错误码、message 记在错误日志的 `admin_info.upstream_error` 中，只有管理员（admin / root）能在日志详情里看到，普通用户查询时会被删除。前提是开启了错误日志（`ErrorLogEnabled`）。管理员看到的原文中，当前渠道的上游地址仍会被遮住，本次新增的清洗规则不作用于这里 |
| 服务端日志 | `channel error (channel #x, status code: xxx)` 记录的仍是原始错误 |

## 3. 未实施：403 改写

ltapi 把所有上游 403 都改写成 529 overloaded。本次**没有实施**，原因是 403 的含义比 401 / 404 复杂，需要先讨论。

当前 403 的处理（未改动）：

- 默认原样透传；
- 正文命中余额类关键词（所有上游）或组织 / 账号被禁类关键词（Claude 规则下）时，改写成 529 / 503，并带 `Retry-After: 5`。

需要讨论的点：

| 讨论点 | 说明 / 选项 |
|---|---|
| ① 改写范围 | A. 所有 403 都改写，只排除违规类（接近 ltapi，覆盖面最大）<br>B. 只改写能识别的账号 / 额度 / 权限类 403，其余透传（即现状，补充关键词）<br>C. 只对 Claude 规则生效，其他上游透传 |
| ② 违规类 403 的识别 | 内容违规、审核拦截的 403 是客户端请求本身的问题，换渠道、重试都没用。改成 529 会让客户端反复重试一个注定失败的请求。只能靠关键词识别：`policy`、`violat`、`content filter`、`moderation`、`safety`、`违规`、`敏感` 等。漏判会导致违规请求被反复重试，误判会让渠道账号问题透传给客户端 |
| ③ 违规类 403 透传时的形状 | 保持 403 原样透传，或者统一改成 400 `invalid_request_error`，更明确地告诉客户端不要重试 |
| ④ 上游 403 的实际构成 | 有的中转上游会把限流、余额不足、账号封禁都包装成 403，也有把内容审核包装成 403 的。我们渠道以哪一类为主，决定选 A 还是 B |

ltapi 文档第 9 节把「所有 403 一律当额度耗尽，没有区分权限型 403」列为已知瑕疵，不建议照搬。初步倾向是 **A + 违规类排除**，上线后根据 `admin_info.upstream_error` 中的实际数据补充关键词。

## 4. 与 ltapi 第 4 节仍有的差异

| 项目 | ltapi | 当前 | 
|---|---|---|
| 403 | 529 + Retry-After | 见第 3 节，未实施 |
| 502 | 503 overloaded + Retry-After | 原样透传 502（正文经过清洗） |
| 连接上游失败 | 503 overloaded | 500 "upstream error: do request failed" |
| 首字节超时 | 走 502 → 503 | 不区分客户端格式，一律 529 "Overloaded"，不带 Retry-After |
| 上游失效正文特征 | 所有上游都生效，含 200 + error 包络 | 只在 Claude 规则下生效；非流式 200 + error 包络会以 HTTP 200 返回给客户端，而且不换渠道 |
| 空 200 | 503 + `Retry-After: 3` | 不处理 |
| 网关内部 panic | 503 overloaded | 500 "Panic detected…"，并带 GitHub issue 链接 |
| 选不到渠道 | 429 + Retry-After | 503 "The model is currently unavailable…"，不带 Retry-After |
| OpenAI 格式的 `metadata` | 不透传 | 原样透传，并被拼进 message（可能含 `provider_name`、原始错误、上游地址） |
| OpenAI 格式的 `type` / `code` | 统一为标准值 | 原样透传上游的值，例如下游 new-api 的 `new_api_error`、`do_request_failed` |
| Gemini 格式的错误形状 | `{"error":{"code":503,"status":"UNAVAILABLE",…}}` | 返回 OpenAI 形状 |
| 主机名黑名单 | 所有账号的 base_url | 只遮住当前渠道的地址 |
| AWS SDK 外壳 | 去掉 | 只遮住 RequestID 的值 |
| request id 指纹 | 使用 `x-request-id` | 响应头 `X-Oneapi-Request-Id`，message 末尾追加 `(request id: …)`，两者都是 new-api 的特征 |
| 错误观测 | 独立的错误事件表，记上游码、下游码、尝试次数、是否重试 | 错误日志 + `admin_info.upstream_error` |

## 5. 测试

测试都在 `service/upstream_error_normalize_test.go` 中：

| 测试 | 覆盖内容 |
|---|---|
| `TestNormalizeUpstreamError` | 401 / 404 / 500 在 Claude 格式和其他格式下分别得到 529 和 503；404 HTML 页面得到 529；所有 Overloaded 结果都带 `Retry-After: 5`，HTML 改写的 "Internal server error" 不带；原始错误保留给管理员 |
| `TestNormalizeUpstreamErrorLeavesOtherErrorsUnchanged` | `/v1/tools/fetch` 的 404、白名单优先于 404、上游 403 都原样透传；网关自己的错误不受影响 |
| `TestUpstreamErrorRetryAfterFollowsTheFinalError` | 后一次尝试返回别的错误时，不会带上前一次的 `Retry-After` |
| `TestNormalizeUpstreamErrorRedactsPassedOnErrors` | 10 种上游标识的清洗；状态码、不重试设置保持不变；原始错误不被修改；3 条正常文案不被误伤 |

运行结果：

```
go test ./service/... ./relay/... ./controller/... ./middleware/... -count=1   # 全部通过
```

`controller/relay.go` 中写入 `Retry-After` 响应头的代码没有自动化测试覆盖：现有测试不具备完整走一遍 `Relay` 的夹具。
