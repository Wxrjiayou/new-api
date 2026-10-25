package controller

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Agents are read-only observers of the accounts root assigns to them. Their
// own log list, log stat, and dashboard keep using the self routes, which
// branch into the scoped handlers below; the owned account list lives here
// under /api/agent.

// GetAgentUsers lists the accounts the calling agent owns.
func GetAgentUsers(c *gin.Context) {
	agentID, ok := service.AgentScopeFromContext(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Only agents can view owned users"))
		return
	}
	pageInfo := common.GetPageQuery(c)
	users, total, err := model.GetAgentOwnedUsers(agentID, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)
	common.ApiSuccess(c, pageInfo)
}

// GetAgentUser returns one account the calling agent owns. Any other id is
// reported as missing, so agents cannot probe which accounts exist.
func GetAgentUser(c *gin.Context) {
	agentID, ok := service.AgentScopeFromContext(c)
	if !ok {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Only agents can view owned users"))
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	user, err := model.GetAgentOwnedUser(agentID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusNotFound, common.NewMessage("User does not exist"))
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, user)
}

// GetAgentRoster lets root check which accounts an agent owns.
func GetAgentRoster(c *gin.Context) {
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorStatus(c, http.StatusForbidden, common.NewMessage("Only the super administrator can view agent rosters"))
		return
	}
	agentID, err := strconv.Atoi(c.Query("agent_id"))
	if err != nil || agentID <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	pageInfo := common.GetPageQuery(c)
	users, total, err := model.GetAgentOwnedUsers(agentID, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(users)
	common.ApiSuccess(c, pageInfo)
}

// getAgentUserLogs serves the self log list for an agent: its own logs and
// those of every account it owns, filterable by username.
func getAgentUserLogs(c *gin.Context, agentID int, filter model.LogFilter, pageInfo *common.PageInfo) {
	userIDs, err := model.AgentScopedUserIDs(agentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter.Username = c.Query("username")
	logs, total, err := model.GetAgentLogs(userIDs, filter, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// streamAgentLogs is the self log export for an agent, over the same users as
// getAgentUserLogs.
func streamAgentLogs(c *gin.Context, ctx context.Context, agentID int, filter model.LogFilter, fn func(*model.Log) error) error {
	userIDs, err := model.AgentScopedUserIDs(agentID)
	if err != nil {
		return err
	}
	filter.Username = c.Query("username")
	return model.StreamAgentLogs(ctx, userIDs, filter, fn)
}

// getAgentLogsStat is the self log stat for an agent. It never filters by
// channel, which agents do not see.
func getAgentLogsStat(c *gin.Context, agentID int, startTimestamp int64, endTimestamp int64) {
	userIDs, err := model.AgentScopedUserIDs(agentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stat, err := model.SumAgentUsedQuota(userIDs, startTimestamp, endTimestamp, c.Query("model_name"), c.Query("username"), c.Query("token_name"), c.Query("group"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"quota": stat.Quota,
		"rpm":   stat.Rpm,
		"tpm":   stat.Tpm,
	})
}

// getAgentQuotaDates is the self dashboard data for an agent.
func getAgentQuotaDates(c *gin.Context, agentID int, startTimestamp int64, endTimestamp int64) {
	userIDs, err := model.AgentScopedUserIDs(agentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetAgentQuotaData(userIDs, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
}

// getAgentFlowQuotaDates is the self flow dashboard data for an agent.
func getAgentFlowQuotaDates(c *gin.Context, agentID int, startTimestamp int64, endTimestamp int64) {
	userIDs, err := model.AgentScopedUserIDs(agentID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetAgentFlowQuotaData(userIDs, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, dates)
}
