# 代理（Agent）用户类型 —— 设计文档

> dev 分支（`Wxrjiayou/new-api` fork），2026-10-25。
> 相关：现有用户体系与权限限制梳理见 `user-management-and-admin-authz.md`；上游同步对照清单见 `docs/dev-custom-commits.md`。

---

## 1. 意图

当前账号体系只有普通用户、管理员、超级管理员三种可用角色。管理员是「全站可见」的，没有任何办法把一个管理账号的视野限定在一批指定用户上。

业务上需要一种**代理商**账号：由 root 指定一批用户归属于他，他能查看这批账号的用量和额度以便对账，但**不能修改**这些账号的任何信息。

这是一个**只读的、范围受限的观察者角色**，不是「权限更小的管理员」。

---

## 2. 目标 / 非目标 / 反目标

### 目标

| 编号 | 目标 |
|---|---|
| R1 | 新增账号类型 **agent（代理）**，**仅 root** 可创建，或把已有普通用户设为代理 |
| R2 | root 指定哪些账号归属某个代理，入口在**用户编辑表单**的「所属代理」字段 |
| R3 | 代理的**使用日志**（对话日志 tab）显示「自己 + 名下账号」的记录，**复用普通用户的日志看板** |
| R4 | 代理的**用户看板**只列出名下账号 |
| R5 | 代理对名下账号**只有查看权，没有任何写权限** |
| R6 | 代理点击用户列表的操作列时，提示「代理不能操作用户」 |
| R7 | 代理可以看到名下账号的**额度**（用户列表里的 `quota` / `used_quota`） |
| R8 | 代理被删除或降级时，**名下账号不受任何影响**，照常使用 |
| R9 | 代理的**数据看板**显示「自己 + 名下账号」的用量数据及总量 |

### 非目标

- **绘图日志、任务日志两个 tab 不做作用域。** 业务确认本期只要使用日志（对话日志）。代理打开那两个 tab 只会看到自己的记录（通常为空）—— 这是已知且接受的结果，不是缺陷。
- **充值记录本期不处理。** `top_ups` 的查询保持原样，代理没有充值记录入口。R7 只覆盖额度。
- 不做 per-agent 的能力开关，代理类型的能力固定。
- 不复用 / 不扩展现有邀请返利体系（`inviter_id` / `aff_code` / `aff_quota` / `QuotaForInviter`）。归属由 root 指派，与邀请无关。
- 代理不能创建用户、不能生成邀请链接、不能给名下账号充值或分配额度。
- 代理不接触渠道、模型、兑换码、分组、部署、系统设置等任何其他管理功能。
- 不做代理的多级嵌套（代理的名下不能再有代理）。
- **本期不做三库验证**（见 §3.5）。

### 反目标（即使功能跑通也算失败）

- **让代理看到不属于他的任何数据。** 哪怕只是用户列表里多出一行别人的账号，或图表里多一个用户的用量。
- **让代理具备任何写能力。** 前端藏按钮不算数，后端必须拒绝。
- **让代理看到上游运营信息。** 渠道名、渠道 ID、上游错误诊断属于运营机密。
- **把本次改动做成上游同步时的长期负担。** 见 §3.1。

---

## 3. 约束

### 3.1 合并友好（硬约束）

本仓库是 `QuantumNous/new-api` 的 fork，会持续同步上游 tag。`docs/dev-custom-commits.md` 就是为此维护的冲突对照清单，目前已有 15 个自定义提交。

因此本次改动遵守三条规则：

1. **新逻辑一律放新文件。**
2. **对上游文件只做"最小插入"** —— 加字段、加分支、加 map 条目。不改函数签名、不改结构体字段语义、不做重构。
3. **绝不重排路由分组。** `router/api-router.go` 里的 `adminRoute` / `selfRoute` 分组结构保持原样，上游在里面增删路由时我们不会冲突。

为此接受一处**可控的代码重复**：日志和看板的查询在新文件里各复制约 10 行查询骨架，换 `model/log.go`、`model/usedata.go`、`model/usedata_flow.go` 零改动。这是刻意的取舍。

### 3.2 数据库拓扑

| 表 | 库 | 说明 |
|---|---|---|
| `users` | 主库 `DB` | 承载代理身份与归属关系 |
| `quota_data` | 主库 `DB` | 数据看板来源，**同库，可用子查询** |
| `logs` | **日志库 `LOG_DB`** | **可能是独立实例，甚至 ClickHouse** |

`logs` 与 `users` 跨库，**无法 JOIN**。日志范围过滤只能：先从主库取名下 id 列表，再 `WHERE logs.user_id IN (...)`。

数据看板走 `quota_data`，与 `users` 同库，不受此限制 —— 但为实现一致，统一用同一份 id 列表。

### 3.3 规模

单个代理的名下账号 **< 20**（业务方确认）。

因此不需要 Redis 缓存 id 列表，不需要分批查询。`logs.user_id` 已有单列索引和复合索引 `idx_user_id_id`，`quota_data.user_id` 也有索引，二十个 id 的 `IN` 查询走索引，无性能问题。仍设一个 sanity cap（100）防御异常数据。

### 3.4 其他

- 代码必须同时兼容 SQLite、MySQL ≥ 5.7.8、PostgreSQL ≥ 9.6（**写法上**遵守，验证见 §3.5）。
- 所有前端文案走 i18n，七个 locale 文件都要加。
- 遵守 `AGENTS.md` / `web/AGENTS.md` 的既有规范。

### 3.5 三库验证本期不做（已决定，须如实记录）

`AGENTS.md` 要求任何影响数据库行为的改动必须在真实的 SQLite / MySQL / PostgreSQL 实例上验证，且这是完成的前提。业务方决定**本期不做**。

因此本次交付必须遵守 `AGENTS.md` 对这种情况的规定：

> 「如果任何必需的数据库验证无法执行，必须明确报告该阻塞项，不得声称该改动数据库兼容或已完成。」

落地时的具体要求：

- 交付说明 / PR 描述里**明确写出「未执行三库验证」**，不得以「构建通过」「单测通过」代替；
- **不得声称**该改动已验证数据库兼容；
- 迁移写法仍须遵守 §3.4 的约束（只用 `ADD COLUMN`，不用 `ALTER COLUMN`，不用任何单库特性），把风险压到最低；
- 建议至少在本地 SQLite 上跑两遍启动验证幂等 —— 这是成本最低的一道防线，不构成三库验证，但能挡住明显错误。

---

## 4. 方案选择

讨论过三个方案，差别在对上游文件的改动面。

### 方案 A — 全新独立端点

新建一组 `/api/agent/*` 端点承载代理的日志、看板和用户列表，前端新建对应页面。

- 上游文件几乎不碰，合并风险最低。
- 但日志和看板都要新建一套前端页面，违背「复用普通用户看板」的要求。

### 方案 B — 完全复用现有端点

改 `model/log.go` 与 `model/usedata*.go` 的 self 查询、改 `model/user.go` 的 `GetAllUsers`/`SearchUsers` 签名、把若干路由从 `adminRoute` 挪到新的 `AgentAuth` 分组。

- UI 完全复用，前端改动最少。
- 但要实质修改近十个上游热点文件。其中**路由分组重排最危险**：上游只要在 `adminRoute` 增删一条路由就会冲突，而冲突解错的后果是越权。

### 方案 C — 混合 ← **已选定**

- **日志与数据看板走复用**：代理 role=5 < admin，前端天然请求 `/api/log/self`、`/api/data/self`、`/api/data/flow/self`。在对应的 controller 处理器各插一个分支，真正的查询写在新文件里。**不改任何 model 函数签名。**
- **用户看板走新端点**：新建只读的 `/api/agent/*`，`router/api-router.go` 只加一行注册，**完全不碰 `adminRoute` 分组**。

**选定理由**：日志与看板 UI 复用的要求拿到了；风险最高的两块（降级管理员路由、在 admin 用户表格里逐个禁用写操作）被整个绕开。

用户看板本来也要把编辑、禁用、删除、改额度全部禁掉再加提示 —— 与其改造 admin 表格再逐一阉割（漏禁一个就是越权），不如直接写一个只读列表，代码量相当，但**不可能漏禁**。

---

## 5. 数据模型

### 5.1 角色常量

`common/constants.go`：

```go
RoleGuestUser  = 0
RoleCommonUser = 1
RoleAgentUser  = 5    // 新增
RoleAdminUser  = 10
RoleRootUser   = 100
```

同时在 `IsValidateRole` 的白名单里加入 `RoleAgentUser`，否则 `authHelper` 会把代理账号判为非法用户、登录时直接 401。

**为什么是 5（低于 admin）**：全仓库有 37 处 `>= RoleAdminUser` 的比较。放在 10 以下，代理默认不具备任何管理能力，是 fail-closed 的。反之若把代理做成 role=10 加权限减项，它会自动继承兑换码、模型、部署、分组、供应商、订阅等全部 `AdminAuth` 路由，必须逐条 deny 去收 —— fail-open，漏一条就是越权。

### 5.2 归属字段

`model.User` 增加一列：

```go
AgentId int `json:"agent_id" gorm:"type:int;default:0;index;column:agent_id"`
```

| 属性 | 取值 |
|---|---|
| 含义 | 该用户归属的代理账号 id |
| `0` | 不归属任何代理（默认值，所有存量用户） |
| 索引 | 单列索引，用于 `WHERE agent_id = ?` |

**服务层约束**（不进数据库，避免三库差异）：

1. `agent_id` 只能指向 `role = RoleAgentUser` 且未被软删除的账号；
2. 只有 `role = RoleCommonUser` 的用户可以被指派（代理、管理员、root 不能有上级代理）；
3. 禁止自指（`agent_id != id`）；
4. **只有 root 能写这个字段**，其他角色提交时静默忽略。

`model.User.EditWithTx` 用的是显式更新字段 map，`UpdateSelf` 走白名单 —— 所以 `agent_id` 不会被意外写入，也**能**被显式置零（R8 需要）。

**为什么不用 `inviter_id`**：那是邀请返利语义，有 `aff_quota` / `aff_count` / `aff_history` 和 `QuotaForInviter` / `QuotaForInvitee` 一整套分成账务挂在上面，root 手工指派会污染分成数据。业务方也明确要求不复用该体系。

**为什么不用关联表**：一个用户只归属一个代理，没有多对多诉求；单列加索引的查询计划更简单，也不用维护第二张表的生命周期。

### 5.3 迁移

`model/main.go:342` 的 `DB.AutoMigrate(&User{}, ...)` 会自动发 `ALTER TABLE users ADD COLUMN agent_id` 并建索引。三个库都走 `ADD COLUMN`（不是 SQLite 不支持的 `ALTER COLUMN`），符合 `AGENTS.md` 的迁移写法约束。

存量数据全部默认 `agent_id = 0`，等价于「无归属」，**升级后行为与升级前完全一致**。

验证范围见 §3.5。

---

## 6. 后端设计

### 6.1 新增文件

#### `model/agent.go`

```go
const AgentMaxOwnedUsers = 100 // sanity cap，业务实际 < 20

// 名下账号 id 列表（含软删除账号，见下方说明）
func GetAgentOwnedUserIDs(agentID int) ([]int, error)

// 数据可见范围 = 代理自己 + 名下账号（R3 / R9）
func AgentScopedUserIDs(agentID int) ([]int, error)

// 名下账号列表（只 Select 展示需要的列，见 §6.4）
func GetAgentOwnedUsers(agentID int, pageInfo *common.PageInfo) ([]*User, int64, error)

// §5.2 的四条约束
func ValidateAgentAssignment(tx *gorm.DB, userID, agentID int) error

// R8：解除该代理的全部归属，不动名下账号本身
func ClearAgentAssignments(tx *gorm.DB, agentID int) error
```

`GetAgentOwnedUserIDs` 超过 `AgentMaxOwnedUsers` 时返回明确错误，不生成超长 SQL。

**软删除账号必须包含在 id 列表里**（用 `Unscoped()`）。否则 root 一删除某个名下账号，该账号的全部历史用量会从代理的日志和图表里**追溯性消失**，上个月的对账数字会变。上游的 `GetAllUsers` / `SearchUsers` 本身也是 `Unscoped()` 的，前端靠 `isUserDeleted(user)` 标记已删除行 —— 代理的用户列表沿用同样做法：**列出但标记为已删除，且操作列照常禁用**。

#### `model/agent_log.go`

对应 `controller/log.go` 的 self 路径。注意 `SearchAllLogs` / `SearchUserLogs` 已是废弃的空实现（直接返回 "This API is deprecated"），不用管。

```go
func GetAgentLogs(userIDs []int, filter LogFilter, startIdx, num int) ([]*Log, int64, error)
func SumAgentUsedQuota(userIDs []int, logType int, startTimestamp, endTimestamp int64, modelName, tokenName string, channel int, group string) (Stat, error)
func StreamAgentLogs(ctx context.Context, userIDs []int, filter LogFilter, fn func(*Log) error) error
```

实现分别对照 `model/log.go` 的 `GetUserLogs`（:592）、`SumUsedQuota`（:689）、`StreamUserLogs`（:648），把 `Where("logs.user_id = ?", userId)` 换成 `Where("logs.user_id IN ?", userIDs)`。

⚠️ **`SumUsedQuota` 是按 `username` 字符串过滤的**（`controller/log.go:230` 的 `GetLogsSelfStat` 传的是 `c.GetString("username")`）。代理路径**必须改用 `user_id`** —— 用户名可被修改，且代理需要匹配多个用户。这是最容易写错的一处。

字段可见性沿用 self 路径的 `logOtherVisibilityUser`，**并额外清空 `Log.ChannelId` / `Log.ChannelName`**（这两个是顶层字段，不在 `Other` 里）。

#### `model/agent_usedata.go`（R9）

```go
// 对照 model/usedata.go:162 GetQuotaDataByUserId
func GetAgentQuotaData(userIDs []int, startTime, endTime int64) ([]*QuotaData, error)

// 对照 model/usedata_flow.go:43 getSelfFlowQuotaData
func GetAgentFlowQuotaData(userIDs []int, startTime, endTime int64) ([]*FlowQuotaData, error)
```

`GetQuotaDataByUserId` 已经 `Group("user_id, username, model_name, created_at")` 并返回 `user_id` + `username`，所以只要把 `user_id = ?` 换成 `user_id IN ?`，前端就能同时拿到**按用户拆分的明细**和**可聚合的总量**（R9 的「以及总量」由前端对返回行求和，与现有 self 视图一致，无需新端点）。

`GetAgentFlowQuotaData` 对照 `getSelfFlowQuotaData`：改 `IN`，并在 `Select` / `Group` 里补上 `user_id, username`，这样流量图也能按用户区分。

⚠️ **绝对不要调用 `fillFlowChannelNames`**（那是 admin/root 路径用的，`model/usedata_flow.go:132`）。只调 `fillFlowTokenNames`，与 self 路径保持一致 —— 否则直接违反「代理不得看到上游运营信息」这条反目标。

#### `service/agent_scope.go`

```go
// 当前请求者是代理时返回其 id，否则返回 0, false
func AgentScopeFromContext(c *gin.Context) (int, bool)
```

所有插入点统一调这一个函数，保证判定逻辑只有一处。

#### `controller/agent.go`

```go
func GetAgentUsers(c *gin.Context)  // R4 名下账号列表
func GetAgentUser(c *gin.Context)   // 单个名下账号详情
func GetAgentRoster(c *gin.Context) // root 查看指定代理的名册，见下
```

前两个先走一个内部 helper：非 agent 角色直接 403；查询的 id 不在名下时返回 **404**（不是 403，避免被用来探测用户是否存在）。

`GetAgentRoster` 是 **root 专用**（handler 内判 `role == RoleRootUser`），接受 `?agent_id=`，返回该代理当前的名册。R2 只给了 root 一个「所属代理」的单用户输入框，没有任何办法回头查证某个代理名下都有谁、也看不出是否接近 `AgentMaxOwnedUsers`。

> 为什么不给 admin 用户列表加 `agent_id` 筛选：`model.SearchUsers(keyword, group, role *int, status *int, startIdx, num, sortOptions ...)` 是位置参数加尾部变参，加一个筛选条件就必须改上游签名，违反 §3.1 规则 2。放在新端点里零上游代价。

#### `router/agent-router.go`

照抄现有 `registerAuthzRoutes` 的模式：

```go
func registerAgentRoutes(apiRouter *gin.RouterGroup) {
    agentRoute := apiRouter.Group("/agent")
    agentRoute.Use(middleware.UserAuth())
    {
        agentRoute.GET("/users", controller.GetAgentUsers)
        agentRoute.GET("/users/:id", controller.GetAgentUser)
        agentRoute.GET("/roster", controller.GetAgentRoster)
    }
}
```

挂 `UserAuth()` 而不是新建 `AgentAuth()` 中间件：少改一个上游文件，且 handler 内部本来就要判角色。

### 6.2 上游文件插入点清单

这是本次改动对上游代码的**全部**接触面。每一项都是插入，不是重构。

| 文件 | 位置 | 改动 | 规模 |
|---|---|---|---|
| `common/constants.go` | :206 | 加 `RoleAgentUser = 5`；`IsValidateRole` 加白名单 | 2 行 |
| `model/user.go` | :114 后 | `User` 结构体加 `AgentId` 字段 | 1 行 |
| `model/user.go` | :253 | 侧边栏默认配置加 agent 分支 | ~8 行 |
| `model/audit_log.go` | :103 | `switch entry.ActorRole` 白名单加 `RoleAgentUser` | 1 行 |
| `model/audit_log.go` | :150 | `actor_role IN (1,10)` 加 `5` | 1 行 |
| `model/audit_log.go` | :235 | 同上 | 1 行 |
| `controller/log.go` | `GetUserLogs` :75 | 分支调 `model.GetAgentLogs` | 3 行 |
| `controller/log.go` | `GetLogsSelfStat` :230 | 分支调 `model.SumAgentUsedQuota` | 3 行 |
| `controller/log.go` | `exportLogs` :115 | `all==false` 时分支调 `model.StreamAgentLogs` | 3 行 |
| `controller/usedata.go` | `GetUserQuotaDates` :63 | 分支调 `model.GetAgentQuotaData` | 3 行 |
| `controller/usedata.go` | `GetUserFlowQuotaDates` :104 | 分支调 `model.GetAgentFlowQuotaData` | 3 行 |
| `controller/user.go` | `CreateUser` :1019 | 创建 agent 必须是 root；step-up 触发阈值降到 `RoleAgentUser` | ~6 行 |
| `controller/user.go` | `UpdateUser` :674 | root 调用时接受 `agent_id`，走 `ValidateAgentAssignment` | ~8 行 |
| `controller/user.go` | `ManageUser` :1118 | 新增 root-only 的 `set_agent` action；`demote` 支持 agent→common 并在同事务解绑 | ~20 行 |
| `controller/user.go` | `DeleteUser` :935 | 删除 agent 时在同事务解绑（R8） | ~4 行 |
| `controller/user.go` | `calculateUserPermissions` :529 | 加 agent 分支 | ~8 行 |
| `controller/user.go` | `canManageTargetRole` :383 | 加 `myRole < RoleAdminUser → false` 前置（见 §6.3） | 3 行 |
| `service/security_verification.go` | :225 | `context.Role < RoleAdminUser` → `< RoleAgentUser` | 1 行 |
| `service/access_token_scope.go` | :55 附近 | `accessTokenStaticResources` 加 `agent` 资源，`minRole: RoleAgentUser`，仅 read | ~5 行 |
| `middleware/access_token_routes.go` | :43 map | 三条新路由的 scope 规则 | 3 行 |
| `router/api-router.go` | :281 附近 | `registerAgentRoutes(apiRouter)` | 1 行 |

**明确不改**（保持代理拿不到）：

| 位置 | 原因 |
|---|---|
| `service/security_verification.go:381` | 这里判的是**调用者自己**的角色（`state` 来自 `GetUserVerificationState`），root 和 admin 已经以 100 / 10 通过。降到 5 只会让代理有资格申请 `admin.user.*` 的 step-up 证明 —— fail-open，毫无收益 |
| `controller/user.go:1432` | `role >= RoleAdminUser` 才能开渠道/上游告警通知 |
| `service/user_notify.go:46` | `role >= RoleAdminUser` 才收管理员通知 |
| `middleware/auth.go:89` | 审计兜底阈值。代理的新端点全是 GET，`beginAdminAudit` 本来就只处理写方法 |
| `controller/midjourney.go` / `controller/task.go` | 绘图日志、任务日志 tab 本期不做作用域（§2 非目标） |
| `model/topup.go` / `controller/topup.go` | 充值记录本期不处理（§2 非目标） |
| `model/user.go` 的 `SearchUsers` 签名 | 见 §6.1 `GetAgentRoster` 的说明 |

### 6.3 R5：只读是后端硬边界

`/api/user/*` 的所有写路由保持在 `AdminAuth()` 之下。代理 role=5 < 10，`authHelper` 直接返回 403 `AUTH_INSUFFICIENT_PRIVILEGE`。

**前端禁用按钮只是 UX，不是安全依赖。**

额外加固 `canManageTargetRole`：

```go
func canManageTargetRole(myRole int, targetRole int) bool {
    if myRole < common.RoleAdminUser {
        return false
    }
    return myRole == common.RoleRootUser || myRole > targetRole
}
```

现在 `canManageTargetRole(5, 1)` 返回 **true**。代理目前到不了这些 handler，但将来谁要是把某条写路由降到更低的门槛，这就是直接越权。

### 6.4 R4/R7：代理能看到的字段

名下账号列表只返回：

```
id, username, display_name, remark, group, status, quota, used_quota,
request_count, created_at, last_login_at, deleted_at(用于标记已删除)
```

**不返回**：`email`、`github_id`、`wechat_id`、`telegram_id`、`oidc_id`、`linux_do_id`、`discord_id`、`aff_code`、`inviter_id`、`agent_id`、`stripe_customer`、`setting`，以及一切密码/令牌相关字段。

> 默认收紧：代理是外部合作方，拿到对账需要的用量和额度即可，绑定信息属于用户隐私。这是实现者可以回头调的决定（§9）。

### 6.5 R8：删除 / 降级不影响名下账号

代理被删除或被降级为普通用户时，在**同一个事务**里执行：

```sql
UPDATE users SET agent_id = 0 WHERE agent_id = <agentId>
```

名下账号的状态、额度、令牌、会话**一律不动** —— 这正是 R8 的要求。

必须同事务。否则会留下指向已删除账号的悬空 `agent_id`（`users` 是软删除，id 不会被复用，但这些账号会永远查不到代理，也不会出现在任何人的看板里，成为孤儿）。

### 6.6 角色变更入口与权限归属

- **创建代理**：`CreateUser` 现在只校验 `user.Role < myRole`，意味着管理员（10）也能创建代理（5）。加显式的 root-only 判定。
- **二次验证**：创建代理属于特权账号创建，和创建管理员一样要求 step-up。现在的触发条件是 `user.Role >= RoleAdminUser`，改成 `>= RoleAgentUser`，并配套放宽 `security_verification.go:225`（**只这一处**，见 §6.2）。
- **已有用户升为代理**：`ManageUser` 新增 root-only 的 `set_agent` action（`RoleCommonUser → RoleAgentUser`）。
- **代理降回普通用户**：`demote` 分支加 agent 处理，同事务解绑。
- **`promote` 保持不变**（仍然只升到 admin），避免改动现有语义。
- **`UpdateUser` 的 `updatedUser.Role = originUser.Role`**（强制还原角色，改角色只能走 `/manage`）**保持不变**。

**普通管理员（role 10）可以管理代理账号** —— 业务方已决定，**不加额外的角色限制**。这意味着：

> `canManageTargetRole(10, 5)` 为真，所以管理员可以查看、编辑、禁用、**降级**、**删除**代理账号。而按 R8，降级和删除会在同一事务里把该代理的全部 `agent_id` 清零 —— **一个普通管理员可以清空某个代理的整个名册**。这是明确接受的权衡（§10 风险表）。`ManageUser` 的 `set_agent` 仍限 root，所以管理员**不能**重建名册，只能破坏。

### 6.7 PAT 作用域

新端点要在 `middleware/access_token_routes.go` 的 `accessTokenRouteRules` 里声明，否则会被 fail-closed 拒绝并打 `access token route is not declared` 日志：

```go
"GET /api/agent/users":     accessTokenScopeRule("agent:read"),
"GET /api/agent/users/:id": accessTokenScopeRule("agent:read"),
"GET /api/agent/roster":    accessTokenScopeRule("agent:read"),
```

对应在 `accessTokenStaticResources` 加一个 `agent` 资源（`minRole: RoleAgentUser`，只有 read 动作），这样代理才能把这个 scope 签进自己的 PAT。

日志和看板的 self 路由已有 `usage:read` 规则，**不需要改**。

---

## 7. 前端设计

遵循 `web/AGENTS.md`：先检索复用现有组件，文案全部走 i18n，改完跑 `bun run typecheck` 和涉及文件的 lint。

### 7.1 使用日志（R3）—— 几乎零改动

代理 role=5 < `ROLE.ADMIN`，`resolveLogsViewAccess`（`features/usage-logs/components/usage-logs-provider.tsx:30`）天然返回 `'self'`，前端照常请求 `/api/log/self`，服务端多返几行而已。

**唯一需要改的**：`features/usage-logs/components/columns/common-logs-columns.tsx` 现在用 `isAdmin` 控制列可见性，「用户名」列对普通用户是隐藏的（自视角下恒等于自己）。加一个独立标志，让代理身份下显示用户名列，但**不要**因此打开渠道列等其他管理员列。

绘图、任务两个 tab **不动**（§2 非目标）。

### 7.2 数据看板（R9）—— 几乎零改动

`features/dashboard/api.ts` 按 `isAdmin` 在 `/api/data/self` 与 `/api/data` 之间选择，代理走 self，服务端返回更宽的行集。`QuotaData` 自带 `user_id` / `username`，总量由前端对返回行求和 —— 和现在的 self 视图完全一样的逻辑。

需要检查的点：`features/dashboard/components/overview/overview-dashboard.tsx:482` 和 `flow/flow-charts.tsx:264` 的 `isAdmin` 分支控制了哪些图表元素。代理需要的是**更宽的 self 视图**，不是 admin 视图 —— 不要为了显示多用户而把 `isAdmin` 打开，那会连带放出渠道维度。按用户区分的展示（如果需要）走数据里的 `username` 字段，不走 `isAdmin`。

### 7.3 用户看板（R4/R6/R7）—— 新建只读页面

新建 `features/agent/`：

- 名下账号列表，复用 `@/components/data-table`（先读该目录的 `README.md` 和公开导出）
- 列：用户名、显示名、备注、分组、状态、额度、已用额度、请求数、创建时间、最后登录
- 已软删除的账号列出但标记（复用现有 `isUserDeleted` 的呈现方式）
- **整个页面没有任何写操作**，不引入编辑抽屉、确认弹窗、状态切换
- 操作列渲染禁用态按钮，点击触发 `toast.info(t('Agents cannot manage users'))`（R6）
- **没有充值记录 tab**（§2 非目标）

新建路由 `routes/_authenticated/agent-users/index.tsx`，`beforeLoad` 守卫 `role === ROLE.AGENT`。

> R6 原文是「点击用户最后一列的操作时提示」。既然是全新只读页面，这里给的是明确的禁用反馈，而不是先渲染一个能用的菜单再拦截。

### 7.4 其他

| 文件 | 改动 |
|---|---|
| `lib/roles.ts` | `ROLE.AGENT = 5`，`ROLE_LABEL_KEYS` 加 `'Agent'` |
| `features/users/constants.ts` | `USER_ROLE.AGENT`，角色 badge 配置，筛选器选项 |
| `features/users/components/users-mutate-drawer.tsx` | 新增「所属代理」选择器，**仅 root 可见可改**；选项来自 `SearchUsers?role=5` |
| `features/users/lib/user-form.ts` | 表单 schema 加 `agent_id` + zod 校验 |
| `hooks/use-sidebar-data.ts` | 代理身份下显示「我的用户」入口 |
| `hooks/use-admin.ts` | **不要**把 `isAdmin` 改成 `>= AGENT`，会污染语义导致一片地方跟着放权。需要的话新增独立 hook |
| `i18n/locales/{en,zh,zh-TW,fr,ru,ja,vi}.json` | `Agent`、`Agents cannot manage users`、`Owning agent`、名下账号超限提示等，**七个文件都要加** |

---

## 8. 测试与验证

### 后端

按 `AGENTS.md`「不要为一个小改动散落测试文件」的要求，**集中在一个测试文件**（建议 `controller/agent_user_test.go`），用 `testify/require` + `assert`，优先表驱动：

- 代理的用户列表只含名下账号，不含其他普通用户、其他代理、管理员、root
- 代理查非名下账号详情返回 **404**
- 代理调用每一条用户写接口（create / update / manage / delete / 2FA 重置 / binding 清理）都返回 403
- 代理的日志查询只返回「自己 + 名下」；改归属后立即变化
- 代理的日志统计按 `user_id` 聚合 —— **用户改名后统计仍正确**（防 `SumUsedQuota` 的 username 陷阱）
- 代理日志里 `ChannelId` / `ChannelName` 被清空，`Other` 按 user 级别裁剪
- 代理的数据看板只覆盖「自己 + 名下」，且流量数据**不含渠道名**
- 软删除的名下账号：历史日志和看板数据仍可见，用户列表中标记为已删除
- `agent_id` 四条约束：不能指向非 agent、不能自指、管理员/root 不能被指派、不能指向已删除账号
- 只有 root 能创建代理；管理员创建代理被拒
- 只有 root 能执行 `set_agent`
- 代理被删除 / 降级后，名下账号 `agent_id` 清零，但状态、额度、令牌、会话均不变（R8）
- 名下账号数超过 `AgentMaxOwnedUsers` 时返回明确错误

### 数据库

**本期不执行三库验证**，见 §3.5 —— 包含对交付说明的强制要求。

建议至少在本地 SQLite 上连跑两遍启动，验证 `AutoMigrate` 幂等。

### 前端

- `features/usage-logs/__tests__/`：代理身份下用户名列可见、渠道列不可见
- `features/agent/__tests__/`：列表只渲染名下账号、已删除账号带标记、操作列禁用且点击出现提示
- `bun run typecheck` + 涉及文件的 lint

---

## 9. 留给实现者的决定

以下由实现者自行判断，不需要回头确认：

1. **用户看板是否包含代理自己** —— 默认**不包含**（R4 原文是「只显示名下账号」）。日志和数据看板**包含**自己，这是业务方明确要的。
2. **可见字段范围**（§6.4）—— 默认收紧到对账所需。实现中若发现业务上确实需要某个字段，可以放开并在交付说明里记一笔。
3. **`AgentMaxOwnedUsers` 的具体数值** —— 100 是防御性取值，业务实际 < 20。
4. **「所属代理」选择器的数据来源** —— 复用 `SearchUsers?role=5`，还是新开一个轻量接口。
5. **代理的日志导出** —— `/api/log/self/export` 已在 R3 范围内，默认允许代理导出自己 + 名下的日志。注意该接口受 `operation_setting.GetLogSetting().UserExportEnabled` 开关控制，代理沿用同一开关。
6. **代理的时间范围限制** —— self 路径对 `role < RoleAdminUser` 应用 `limitLogTimeRange`，代理默认沿用同一限制（不特殊放宽）。
7. **`GetAgentRoster` 的返回形状** —— 复用名下账号列表的字段集即可，root 视角可以多返 `email` 等字段。
8. **流量看板是否按用户拆分展示** —— 数据层已带 `username`，前端呈现方式由实现者定；但**不得**通过打开 `isAdmin` 来实现。

---

## 10. 风险

| 风险 | 说明 | 处置 |
|---|---|---|
| **未做三库验证** | 这是 schema 变更，`AGENTS.md` 要求的验证本期不做 | 业务方已决定。按 §3.5 在交付说明里明确声明未验证，**不得声称数据库兼容** |
| **管理员可清空代理名册** | `canManageTargetRole(10, 5)` 为真，admin 降级/删除代理会连带清零全部 `agent_id`，且 `set_agent` 限 root 所以 admin 无法重建 | 业务方已明确接受（§6.6）。建议至少保证这类操作进审计（`model/audit_log.go` 的角色白名单改动已覆盖） |
| **新增角色的漏网比较** | 全仓库 37 处 `RoleAdminUser` 比较，加一档角色后每处都可能需要重新审视 | §6.2 已逐条列出。实现时再全量 `grep` 一遍 `RoleCommonUser` 和 `RoleAdminUser` 复核，特别留意 `== RoleCommonUser` 这种等值判断 |
| **`SumUsedQuota` 按 username 过滤** | 代理路径必须改用 `user_id`，否则用户改名后统计出错 | §6.1 已点明，§8 有专门测试用例 |
| **`fillFlowChannelNames` 误用** | 流量看板的 admin/root 路径会填渠道名，代理路径误调就直接泄露上游信息 | §6.1 已点明，§8 有测试用例 |
| **双份侧边栏配置** | `controller/user.go:529` 和 `model/user.go:253` 有重复的默认配置逻辑 | 两处同步改。**不要**顺手合并重构 —— 那会增加上游合并难度，违反 §3.1 |
| **查询代码重复** | `model/agent_log.go`、`model/agent_usedata.go` 复制了上游五处查询的骨架 | 刻意取舍（§3.1）。上游若修改这些查询的过滤逻辑，需同步 —— 落地后在 `docs/dev-custom-commits.md` 记一笔提醒 |
| **绘图/任务日志 tab 范围不一致** | 代理在这两个 tab 只看到自己的记录 | 已知且接受（§2 非目标）。若用户反馈困惑，可考虑对代理隐藏这两个 tab —— 成本远低于做作用域 |
