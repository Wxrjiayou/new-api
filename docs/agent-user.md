# 代理（Agent）用户类型：实现说明

- 分支：`feat/agent-user`（从 `dev` 的 `425d0b2b3` 切出）
- 依据：`docs/agent-user-type-design.md`
- 提交：

| 提交 | 内容 |
|---|---|
| `01565199d` | 后端：角色、归属字段、只读端点、日志与看板分支、解绑、测试 |
| `20dc4893c` | 前端：用户管理、代理用户页、日志页适配、侧边栏、多语言 |

## 1. 概述

代理是一个**只读观察者**，角色值 `5`，介于普通用户（1）和管理员（10）之间：

- 由 root 创建，或由 root 把普通用户设为代理；名下账号由 root 在用户编辑表单里通过 `agent_id` 指定。
- 能看到：自己和名下账号的使用日志、日志统计、数据看板，以及名下账号列表（含额度、已用额度）。
- 看不到：渠道 id / 渠道名等上游信息。
- 不能做：任何写操作。代理拿不到管理权限，因为 role 5 低于所有管理端的 `>= RoleAdminUser` 判断，出错时默认是关着的。
- 不嵌套：只有普通用户能归属代理，代理和管理员都不能被归属。
- 每个代理最多名下 100 个账号（`AgentMaxOwnedUsers`），保证名下 id 能放进一条 `IN` 查询。

实现走设计文档里的方案 C：日志和看板复用现有 self 路由，在 controller 里加分支；名下用户列表开新端点 `/api/agent/*`。不改上游函数签名，便于后续同步上游。

## 2. 数据模型

`model/user.go` 新增字段：

```go
AgentId int `json:"agent_id" gorm:"type:int;default:0;index;column:agent_id"`
```

`0` 表示不属于任何代理。字段由 AutoMigrate 加列，三库都是普通 int 列加索引。

`UpdateWithTx` 的 `Omit` 列表加入 `agent_id`，普通的用户编辑不会顺带改写归属，归属只能经由下面两个函数修改：

```go
"agent_id", // owned by AssignUserAgentWithTx / ClearAgentAssignments
```

## 3. 角色常量

`common/constants.go`：

```go
const (
	RoleGuestUser  = 0
	RoleCommonUser = 1
	RoleAgentUser  = 5
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

func IsValidateRole(role int) bool {
	return role == RoleGuestUser || role == RoleCommonUser || role == RoleAgentUser || role == RoleAdminUser || role == RoleRootUser
}
```

不把 5 加进 `IsValidateRole`，代理登录会被鉴权中间件直接拒绝。审计日志的 actor role 白名单（`model/audit_log.go` 三处）也同步加了 `RoleAgentUser`，否则代理的操作会被记成未知角色。

## 4. 归属范围（model/agent.go）

所有代理视图都从同一个"可见用户集合"出发：代理自己 + 名下账号。名下账号用 `Unscoped()` 查，**软删除的账号也算在内**，删号后历史日志和看板数据不会消失。

```go
func GetAgentOwnedUserIDs(agentID int) ([]int, error) {
	var ids []int
	err := DB.Unscoped().Model(&User{}).Where("agent_id = ?", agentID).
		Limit(AgentMaxOwnedUsers+1).Pluck("id", &ids).Error
	if err != nil {
		return nil, err
	}
	if len(ids) > AgentMaxOwnedUsers {
		return nil, common.NewMessage("This agent owns more than {{max}} users", map[string]any{"max": AgentMaxOwnedUsers})
	}
	return ids, nil
}

func AgentScopedUserIDs(agentID int) ([]int, error) {
	ids, err := GetAgentOwnedUserIDs(agentID)
	if err != nil {
		return nil, err
	}
	return append(ids, agentID), nil
}
```

代理能看到的用户字段由 `AgentOwnedUser` 白名单限定（id、用户名、显示名、备注、分组、状态、额度、已用额度、请求数、创建/登录时间、删除时间），不含邮箱、第三方绑定、权限等信息。

### 4.1 指定归属

```go
func AssignUserAgentWithTx(tx *gorm.DB, userID int, agentID int) error {
	if agentID != 0 {
		if agentID == userID {
			return common.NewMessage("A user cannot be its own agent")
		}
		// 被归属者必须是普通用户 → 禁止嵌套
		if user.Role != common.RoleCommonUser {
			return common.NewMessage("Only common users can belong to an agent")
		}
		// 归属目标必须是存在的代理
		if err != nil || agent.Role != common.RoleAgentUser {
			return common.NewMessage("The owning agent must be an agent account")
		}
		// 名下数量上限
		if owned >= AgentMaxOwnedUsers { ... }
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update("agent_id", agentID).Error
}
```

（省略了查询细节，完整代码见 `model/agent.go`。）

### 4.2 解绑

```go
func ClearAgentAssignments(tx *gorm.DB, agentID int) error {
	return tx.Unscoped().Model(&User{}).Where("agent_id = ?", agentID).Update("agent_id", 0).Error
}
```

只清 `agent_id`，名下账号的状态、额度、令牌都不动。调用点都在已有事务里，保证不会出现悬空归属：

- `ManageUser` 的 `demote` 分支，和 `authz.ClearUserAuthorizationInTx` 同一事务
- `User.delete`（软删除）
- `User.HardDelete`

## 5. 权限控制（controller/user.go）

### 5.1 代理不能管理任何人

`canManageTargetRole` 原本是 `myRole == root || myRole > targetRole`，代理（5）对普通用户（1）会返回 true。加前置判断：

```go
func canManageTargetRole(myRole int, targetRole int) bool {
	if myRole < common.RoleAdminUser {
		return false
	}
	return myRole == common.RoleRootUser || myRole > targetRole
}
```

### 5.2 只有 root 能创建 / 设置代理

创建用户：

```go
if user.Role == common.RoleAgentUser && myRole != common.RoleRootUser {
	common.ApiErrorT(c, "Only the super administrator can create agents")
	return
}
// Creating an administrator or agent grants privilege just like promoting one.
if user.Role >= common.RoleAgentUser {
	authorization := requireAdminUserProof(c, service.VerificationScopeAdminUserCreate, ...)
```

创建代理和创建管理员一样要过二次安全验证；`service/security_verification.go` 里 `admin.user.create` 的最低角色相应从 10 放宽到 5，`adminUserManageActions` 加入 `set_agent`。

`ManageUser` 新增 `set_agent` 动作，并让 `promote` 拒绝代理和已归属账号：

```go
case "promote":
	// Admins never own or belong to a roster; unwind agent state first.
	if user.Role == common.RoleAgentUser || user.AgentId != 0 {
		common.ApiErrorT(c, "Remove the agent relationship before promoting this user")
		return
	}
	user.Role = common.RoleAdminUser
case "set_agent":
	if myRole != common.RoleRootUser { ... }          // 仅 root
	if user.Role != common.RoleCommonUser { ... }     // 仅普通用户
	if user.AgentId != 0 { ... }                      // 已归属的不能当代理
	user.Role = common.RoleAgentUser
```

`demote` 把代理降为普通用户时，在同一事务里解绑名下账号：

```go
if err := user.UpdateWithTx(tx, false); err != nil {
	return err
}
// A demoted agent releases its roster in the same commit.
if err := model.ClearAgentAssignments(tx, user.Id); err != nil {
	return err
}
return authz.ClearUserAuthorizationInTx(tx, user.Id)
```

### 5.3 编辑时指定归属

`UpdateUser` 先读出 `agent_id`，只有 root 的请求会被采纳，其他人传了也忽略：

```go
func readAgentAssignment(c *gin.Context, myRole int) (int, bool, error) {
	if myRole != common.RoleRootUser {
		return 0, false, nil
	}
	var body struct {
		AgentId *int `json:"agent_id"`
	}
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		return 0, false, err
	}
	if body.AgentId == nil {
		return 0, false, nil
	}
	if *body.AgentId < 0 {
		return 0, false, errors.New("invalid agent id")
	}
	return *body.AgentId, true, nil
}
```

用指针区分"没传"和"传 0（解除归属）"。值有变化时在编辑事务内调用 `AssignUserAgentWithTx`，并写进审计参数 `agent_id`。

## 6. 日志与看板：复用 self 路由

判断调用者是否代理统一走一个函数（`service/agent_scope.go`）：

```go
func AgentScopeFromContext(c *gin.Context) (int, bool) {
	if c.GetInt("role") != common.RoleAgentUser {
		return 0, false
	}
	return c.GetInt("id"), true
}
```

在现有 self handler 里插一个分支，非代理路径完全不变：

```go
// controller/log.go GetUserLogs
if agentID, ok := service.AgentScopeFromContext(c); ok {
	getAgentUserLogs(c, agentID, filter, pageInfo)
	return
}
```

同样的插入点：

| 路由 handler | 代理分支 | model 函数 |
|---|---|---|
| `GetUserLogs` | `getAgentUserLogs` | `GetAgentLogs` |
| `exportLogs`（非 all） | `streamAgentLogs` | `StreamAgentLogs` |
| `GetLogsSelfStat` | `getAgentLogsStat` | `SumAgentUsedQuota` |
| `GetUserQuotaDates` | `getAgentQuotaDates` | `GetAgentQuotaData` |
| `GetUserFlowQuotaDates` | `getAgentFlowQuotaDates` | `GetAgentFlowQuotaData` |

代理分支额外支持按 `username` 过滤，用于在名下账号里筛选。

### 6.1 model 查询（model/agent_log.go、model/agent_usedata.go）

这些函数是上游 self 查询的拷贝，把单个 `user_id = ?` 换成 `user_id IN ?`。文件头注释写明了对应的上游函数，同步上游时需要一起改。

日志列表，复用 `formatUserLogs` 再清掉渠道 id：

```go
func formatAgentLogs(logs []*Log, startIdx int) {
	formatUserLogs(logs, startIdx)
	for _, log := range logs {
		log.ChannelId = 0
	}
}

func GetAgentLogs(userIDs []int, filter LogFilter, startIdx int, num int) (logs []*Log, total int64, err error) {
	tx, err := filter.apply(LOG_DB.Where("logs.user_id IN ?", userIDs))
	...
	formatAgentLogs(logs, startIdx)
	return logs, total, err
}
```

导出时同样清掉 `ChannelId`、`ChannelName`，`Other` 按普通用户可见性过滤：

```go
log.ChannelId = 0
log.ChannelName = ""
log.Other = formatLogOtherJSON(log.Other, logOtherVisibilityUser)
```

日志统计按 `user_id` 匹配，不像上游 `SumUsedQuota` 那样按 username（用户名可改）；也不接受 `channel` 参数：

```go
tx := LOG_DB.Table("logs").Select("COALESCE(sum(quota), 0) quota").Where("user_id IN ?", userIDs)
```

数据看板按用户拆分行，前端既能按用户看明细也能汇总：

```go
DB.Table("quota_data").
	Select("user_id, username, model_name, created_at, sum(count) as count, ...").
	Where("user_id IN ? and created_at >= ? and created_at <= ?", userIDs, startTime, endTime).
	Group("user_id, username, model_name, created_at")
```

流量看板在上游 `getSelfFlowQuotaData` 的分组上补了 `user_id, username`，只调用 `fillFlowTokenNames`，不填渠道名：

```go
flowQuotaBaseQuery(startTime, endTime).
	Select("user_id, username, token_id, use_group, model_name, sum(count) as count, ...").
	Where("user_id IN ?", userIDs).
	Group("user_id, username, token_id, use_group, model_name")
```

## 7. 新端点 /api/agent/*

`router/agent-router.go`，挂 `UserAuth`，每个 handler 自己检查角色：

```go
agentRoute := apiRouter.Group("/agent")
agentRoute.Use(middleware.UserAuth())
{
	agentRoute.GET("/users", controller.GetAgentUsers)     // 代理：名下账号分页
	agentRoute.GET("/users/:id", controller.GetAgentUser)  // 代理：单个名下账号
	agentRoute.GET("/roster", controller.GetAgentRoster)   // root：查看某代理名下账号
}
```

`GetAgentUser` 对不属于自己的 id 一律返回 404，代理无法借此探测其他账号是否存在。

Access token 方面，`middleware/access_token_routes.go` 给这三条路由配了 `agent:read` scope；`service/access_token_scope.go` 新增 `agent` 资源，`minRole` 为 `RoleAgentUser`，只有代理及以上能在令牌里勾选。

## 8. 前端

### 8.1 角色与用户管理

`web/src/lib/roles.ts` 加 `AGENT: 5` 和显示名 `Agent`；用户列表的角色筛选路由参数允许 `'5'`。

用户编辑抽屉（`users-mutate-drawer.tsx`）：

- 角色下拉只对 root 显示"代理"选项；创建代理与创建管理员一样走二次验证。
- root 编辑普通用户时出现"所属代理"下拉，选项来自按 role=5 搜索的用户列表，`0` 表示无：

```tsx
const viewerIsRoot = currentUser?.role === ROLE.SUPER_ADMIN
// Only root assigns owning agents, and only common users can be owned.
const canAssignAgent =
  viewerIsRoot && isUpdate && currentRow?.role === ROLE.USER
const { data: agentsData } = useQuery({
  queryKey: ['agent-users'],
  queryFn: async () =>
    requireServerSuccess(
      await searchUsers({ role: String(ROLE.AGENT), p: 1, page_size: 100 })
    ),
  enabled: open && canAssignAgent,
})
```

行操作（`data-table-row-actions.tsx`）与后端规则一一对应：

```tsx
// The backend rejects promoting agents or owned users; root unwinds those first.
const canPromote = !isAdmin && !isAgent && !user.agent_id
const canSetAgent =
  viewerIsRoot && user.role === USER_ROLE.USER && !user.agent_id
```

代理也显示"降级"菜单项。

### 8.2 代理用户页

新路由 `/agent-users`（`routes/_authenticated/agent-users/index.tsx`），页面代码在 `features/agent/`：

- `api.ts`：调用 `GET /api/agent/users`
- `components/agent-users-table.tsx`、`agent-users-columns.tsx`：只读表格，列复用用户管理的单元格；已删除账号用 `lib/user-status.ts` 标记
- `components/agent-user-actions-cell.tsx`：操作列只给提示，没有任何写操作

侧边栏新增 `agent` 分组，只对 role 恰好等于 5 的用户显示，管理员看不到：

```ts
.filter((group) => {
  if (group.id === 'admin') return isAdmin
  if (group.id === 'agent') return role === ROLE.AGENT
  return true
})
```

`routeTree.gen.ts` 由 TanStack router 生成器重新生成，只多出这一条路由。

### 8.3 日志页适配

代理请求的仍是 self 日志接口，但返回的是多个用户的数据，需要显示用户名列和用户名筛选，同时不能出现管理员 UI（渠道列、用户信息弹窗）。为此把原来的 `isAdmin` 拆出一个更窄的 `showUsers`：

```tsx
// usage-logs-provider.tsx
// Agents read the self routes, which also return the users they own, so
// they need the username column and filter without any other admin UI.
const showUsers = isAdminView || role === ROLE.AGENT
```

`buildApiParams` 里 `username` 参数由 `showUsers` 控制，`channel` 仍由 `isAdmin` 控制：

```ts
...(isAdmin && searchParams.channel
  ? { channel: Number(searchParams.channel) || 0 }
  : {}),
...(showUsers && searchParams.username
  ? { username: String(searchParams.username) }
  : {}),
```

`showUsers` 默认等于 `isAdmin`，非代理的行为不变。用户名单元格拆到 `log-user-cell.tsx`，只有管理员点击会打开用户信息弹窗。

### 8.4 多语言

新增 20 个文案，7 个语言文件都补齐。后端 `common.NewMessage` 的英文 key 也经由前端语言文件翻译。没有跑 `i18n:sync`，因为它还会补上 `dev` 上其他提交遗漏的 key，超出本次范围。

## 9. 测试

`controller/agent_user_test.go`：

| 测试 | 覆盖点 |
|---|---|
| `TestAgentSelfLogsCoverOwnedUsersOnly` | 日志只含自己和名下账号（含已删除），不含渠道 id；普通用户不受影响 |
| `TestAgentSelfStatSumsScopedUsersByID` | 统计按 `user_id` 汇总，改用户名后结果不变 |
| `TestAgentDashboardCoversScopedUsers` | 看板只统计名下范围 |
| `TestAgentUsersListsOnlyOwnedAccounts` | 用户看板只列名下账号且不含邮箱等字段，他人 id 和非代理调用都失败 |
| `TestAssignUserAgentRules` | 不能自归属、不能嵌套、目标必须是代理 |
| `TestManageUserSetAgentIsRootOnly` | 只有 root 能 `set_agent` |
| `TestDemotingAgentReleasesRosterOnly` | 降级时解绑，名下账号其他字段不变 |
| `TestDeletingAgentReleasesRoster` | 删除时解绑 |

验证结果：

- `go vet` 与 `go test ./model/... ./controller/... ./middleware/... ./common/...` 通过（默认测试数据库）。
- 前端非测试源码 `tsgo` 类型检查通过；改动文件 oxlint、oxfmt 通过。

## 10. 未验证与已知边界

- **三库兼容**：只在默认测试数据库上跑过，MySQL / PostgreSQL 未验证。
- **前端单元测试**：本次未新增，环境里也没有 vitest，现有前端测试未运行。
- **前端构建**：生产构建因 `node-forge` 缺失失败，`dev` 上同样如此，与本次改动无关。
- **浏览器联调**：未用真实代理账号点过页面。
- **令牌名可见**：流量看板会显示名下用户的令牌名称。这不属于上游信息，但属于设计文档 §6.4 的隐私边界。
- **本期不做**：绘图日志、任务日志、充值记录的代理视图。
