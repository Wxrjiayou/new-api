package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type agentTestFixture struct {
	agent, owned, deleted, stranger model.User
}

// setupAgentTest creates an agent owning one live and one deleted account,
// plus a stranger it does not own, each with one consume log.
func setupAgentTest(t *testing.T) (*gorm.DB, agentTestFixture) {
	t.Helper()
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.QuotaData{}))
	var f agentTestFixture
	f.agent = model.User{Username: "agent", Role: common.RoleAgentUser, Status: common.UserStatusEnabled, AffCode: "agent-aff"}
	require.NoError(t, db.Create(&f.agent).Error)
	f.owned = model.User{Username: "owned", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 700, UsedQuota: 300, AgentId: f.agent.Id, AffCode: "owned-aff"}
	f.deleted = model.User{Username: "deleted", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AgentId: f.agent.Id, AffCode: "deleted-aff"}
	f.stranger = model.User{Username: "stranger", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "stranger-aff"}
	for _, u := range []*model.User{&f.owned, &f.deleted, &f.stranger} {
		require.NoError(t, db.Create(u).Error)
	}
	require.NoError(t, db.Delete(&f.deleted).Error)

	now := time.Now().Unix()
	for i, u := range []model.User{f.agent, f.owned, f.deleted, f.stranger} {
		require.NoError(t, model.LOG_DB.Create(&model.Log{
			UserId: u.Id, Username: u.Username, CreatedAt: now, Type: model.LogTypeConsume,
			ModelName: "gpt-test", Quota: 10 * (i + 1), PromptTokens: 1, ChannelId: 42,
		}).Error)
		require.NoError(t, db.Create(&model.QuotaData{
			UserID: u.Id, Username: u.Username, ModelName: "gpt-test", CreatedAt: now - now%3600, Count: 1, Quota: 10 * (i + 1),
		}).Error)
	}
	return db, f
}

func performAgentRequest(t *testing.T, handler gin.HandlerFunc, role int, userID int, target string, params gin.Params) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	c.Params = params
	c.Set("id", userID)
	c.Set("role", role)
	handler(c)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func agentLogUsernames(t *testing.T, body map[string]any, asAgent bool) []string {
	t.Helper()
	require.Equal(t, true, body["success"], body)
	items := body["data"].(map[string]any)["items"].([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		log := item.(map[string]any)
		if asAgent {
			assert.EqualValues(t, 0, log["channel"], "agents must not see channel ids")
		}
		names = append(names, log["username"].(string))
	}
	return names
}

func TestAgentSelfLogsCoverOwnedUsersOnly(t *testing.T) {
	_, f := setupAgentTest(t)
	body := performAgentRequest(t, GetUserLogs, common.RoleAgentUser, f.agent.Id, "/api/log/self", nil)
	assert.ElementsMatch(t, []string{"agent", "owned", "deleted"}, agentLogUsernames(t, body, true))

	body = performAgentRequest(t, GetUserLogs, common.RoleAgentUser, f.agent.Id, "/api/log/self?username=owned", nil)
	assert.Equal(t, []string{"owned"}, agentLogUsernames(t, body, true))

	// A common user still sees only its own logs, and the username filter is ignored.
	body = performAgentRequest(t, GetUserLogs, common.RoleCommonUser, f.owned.Id, "/api/log/self?username=stranger", nil)
	assert.Equal(t, []string{"owned"}, agentLogUsernames(t, body, false))
}

func TestAgentSelfStatSumsScopedUsersByID(t *testing.T) {
	db, f := setupAgentTest(t)
	// Renaming must not change which logs count: they are matched by user id.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", f.owned.Id).Update("username", "renamed").Error)
	body := performAgentRequest(t, GetLogsSelfStat, common.RoleAgentUser, f.agent.Id, "/api/log/self/stat", nil)
	require.Equal(t, true, body["success"], body)
	assert.EqualValues(t, 10+20+30, body["data"].(map[string]any)["quota"])
}

func TestAgentDashboardCoversScopedUsers(t *testing.T) {
	_, f := setupAgentTest(t)
	now := time.Now().Unix()
	target := fmt.Sprintf("/api/data/self?start_timestamp=%d&end_timestamp=%d", now-7200, now+3600)
	body := performAgentRequest(t, GetUserQuotaDates, common.RoleAgentUser, f.agent.Id, target, nil)
	require.Equal(t, true, body["success"], body)
	users := map[float64]bool{}
	for _, row := range body["data"].([]any) {
		users[row.(map[string]any)["user_id"].(float64)] = true
	}
	assert.Equal(t, map[float64]bool{float64(f.agent.Id): true, float64(f.owned.Id): true, float64(f.deleted.Id): true}, users)
}

func TestAgentUsersListsSelfAndOwnedAccounts(t *testing.T) {
	_, f := setupAgentTest(t)
	body := performAgentRequest(t, GetAgentUsers, common.RoleAgentUser, f.agent.Id, "/api/agent/users", nil)
	require.Equal(t, true, body["success"], body)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, 3, data["total"])
	items := data["items"].([]any)
	require.Len(t, items, 3)
	// The agent's own account comes first so it can follow its own balance.
	assert.Equal(t, "agent", items[0].(map[string]any)["username"])
	for _, item := range items {
		user := item.(map[string]any)
		assert.Contains(t, []string{"agent", "owned", "deleted"}, user["username"])
		assert.NotContains(t, user, "email")
		assert.NotContains(t, user, "access_token")
	}

	body = performAgentRequest(t, GetAgentUser, common.RoleAgentUser, f.agent.Id, "/api/agent/users/x", gin.Params{{Key: "id", Value: fmt.Sprint(f.agent.Id)}})
	assert.Equal(t, true, body["success"], body)

	body = performAgentRequest(t, GetAgentUser, common.RoleAgentUser, f.agent.Id, "/api/agent/users/x", gin.Params{{Key: "id", Value: fmt.Sprint(f.owned.Id)}})
	require.Equal(t, true, body["success"], body)
	assert.EqualValues(t, 700, body["data"].(map[string]any)["quota"])
	body = performAgentRequest(t, GetAgentUser, common.RoleAgentUser, f.agent.Id, "/api/agent/users/x", gin.Params{{Key: "id", Value: fmt.Sprint(f.stranger.Id)}})
	assert.Equal(t, false, body["success"])

	body = performAgentRequest(t, GetAgentUsers, common.RoleCommonUser, f.owned.Id, "/api/agent/users", nil)
	assert.Equal(t, false, body["success"])
}

func TestAssignUserAgentRules(t *testing.T) {
	db, f := setupAgentTest(t)
	admin := model.User{Username: "admin", Role: common.RoleAdminUser, AffCode: "admin-aff"}
	require.NoError(t, db.Create(&admin).Error)

	require.NoError(t, model.AssignUserAgentWithTx(db, f.stranger.Id, f.agent.Id))
	assert.Error(t, model.AssignUserAgentWithTx(db, admin.Id, f.agent.Id), "admins cannot be owned")
	assert.Error(t, model.AssignUserAgentWithTx(db, f.stranger.Id, f.owned.Id), "owner must be an agent")
	assert.Error(t, model.AssignUserAgentWithTx(db, f.agent.Id, f.agent.Id), "no self ownership")
	require.NoError(t, model.AssignUserAgentWithTx(db, f.stranger.Id, 0))
}

func TestManageUserSetAgentIsRootOnly(t *testing.T) {
	db, f := setupAgentTest(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(fmt.Sprintf(`{"id":%d,"action":"set_agent"}`, f.stranger.Id)))
	c.Set("id", 9999)
	c.Set("role", common.RoleAdminUser)
	ManageUser(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)

	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"set_agent"}`, f.stranger.Id))})
	recorder = performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"set_agent"}`, f.stranger.Id), identity, proof)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())
	var updated model.User
	require.NoError(t, db.First(&updated, f.stranger.Id).Error)
	assert.Equal(t, common.RoleAgentUser, updated.Role)
}

func TestDemotingAgentReleasesRosterOnly(t *testing.T) {
	db, f := setupAgentTest(t)
	previousMaster := common.IsMasterNode
	common.IsMasterNode = false
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	require.NoError(t, authz.Init(db))
	identity, proof := manageUserProof(t, db, service.VerificationOperation{Scope: service.VerificationScopeAdminUserManage, Context: []byte(fmt.Sprintf(`{"user_id":%d,"action":"demote"}`, f.agent.Id))})
	recorder := performVerifiedManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"demote"}`, f.agent.Id), identity, proof)
	require.Contains(t, recorder.Body.String(), `"success":true`, recorder.Body.String())

	var agent, owned model.User
	require.NoError(t, db.First(&agent, f.agent.Id).Error)
	assert.Equal(t, common.RoleCommonUser, agent.Role)
	require.NoError(t, db.First(&owned, f.owned.Id).Error)
	assert.Equal(t, 0, owned.AgentId)
	assert.Equal(t, common.UserStatusEnabled, owned.Status)
	assert.Equal(t, 700, owned.Quota)
	var deletedOwner int
	require.NoError(t, db.Unscoped().Model(&model.User{}).Where("id = ?", f.deleted.Id).Pluck("agent_id", &deletedOwner).Error)
	assert.Equal(t, 0, deletedOwner)
}

func TestDeletingAgentReleasesRoster(t *testing.T) {
	db, f := setupAgentTest(t)
	_, err := model.DeleteUserById(f.agent.Id)
	require.NoError(t, err)
	var owned model.User
	require.NoError(t, db.First(&owned, f.owned.Id).Error)
	assert.Equal(t, 0, owned.AgentId)
	assert.Equal(t, common.UserStatusEnabled, owned.Status)
}
