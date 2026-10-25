package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// AgentMaxOwnedUsers bounds how many accounts one agent may own, so the owned
// id list always fits in one IN query against the log database.
const AgentMaxOwnedUsers = 100

// AgentOwnedUser is the part of an owned account an agent may see: what it
// needs to reconcile usage and quota, and none of the account's bindings.
type AgentOwnedUser struct {
	Id           int            `json:"id"`
	Username     string         `json:"username"`
	DisplayName  string         `json:"display_name"`
	Remark       string         `json:"remark"`
	Group        string         `json:"group"`
	Status       int            `json:"status"`
	Quota        int            `json:"quota"`
	UsedQuota    int            `json:"used_quota"`
	RequestCount int            `json:"request_count"`
	CreatedAt    int64          `json:"created_at"`
	LastLoginAt  int64          `json:"last_login_at"`
	DeletedAt    gorm.DeletedAt `json:"DeletedAt"`
}

// agentOwnedUserColumns are field names of User; GORM quotes them, "group"
// included, for every database.
var agentOwnedUserColumns = []string{
	"id", "username", "display_name", "remark", "group", "status", "quota",
	"used_quota", "request_count", "created_at", "last_login_at", "deleted_at",
}

// GetAgentOwnedUserIDs returns the ids of the accounts the agent owns. Deleted
// accounts stay in the list so their past usage does not vanish from the
// agent's logs and dashboard.
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

// AgentScopedUserIDs is the set of users whose usage an agent sees: the agent
// itself and every account it owns.
func AgentScopedUserIDs(agentID int) ([]int, error) {
	ids, err := GetAgentOwnedUserIDs(agentID)
	if err != nil {
		return nil, err
	}
	return append(ids, agentID), nil
}

// GetAgentOwnedUsers pages through the accounts the agent owns, deleted ones
// included and marked by DeletedAt.
func GetAgentOwnedUsers(agentID int, startIdx int, num int) ([]*AgentOwnedUser, int64, error) {
	var total int64
	users := make([]*AgentOwnedUser, 0)
	tx := DB.Unscoped().Model(&User{}).Where("agent_id = ?", agentID)
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := tx.Select(agentOwnedUserColumns).Order("id desc").Limit(num).Offset(startIdx).Find(&users).Error
	return users, total, err
}

// GetAgentOwnedUser returns one account the agent owns, or
// gorm.ErrRecordNotFound when the account is not the agent's.
func GetAgentOwnedUser(agentID int, userID int) (*AgentOwnedUser, error) {
	var user AgentOwnedUser
	err := DB.Unscoped().Model(&User{}).Select(agentOwnedUserColumns).
		Where("id = ? AND agent_id = ?", userID, agentID).Take(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// AssignUserAgentWithTx sets which agent owns userID; agentID 0 removes the
// owner. Only common users can be owned, and only by an existing agent that
// has room for another account.
func AssignUserAgentWithTx(tx *gorm.DB, userID int, agentID int) error {
	if agentID != 0 {
		if agentID == userID {
			return common.NewMessage("A user cannot be its own agent")
		}
		var user User
		if err := tx.Select("id", "role").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Role != common.RoleCommonUser {
			return common.NewMessage("Only common users can belong to an agent")
		}
		var agent User
		err := tx.Select("id", "role").First(&agent, agentID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err != nil || agent.Role != common.RoleAgentUser {
			return common.NewMessage("The owning agent must be an agent account")
		}
		var owned int64
		if err := tx.Unscoped().Model(&User{}).Where("agent_id = ? AND id <> ?", agentID, userID).Count(&owned).Error; err != nil {
			return err
		}
		if owned >= AgentMaxOwnedUsers {
			return common.NewMessage("This agent already owns {{max}} users", map[string]any{"max": AgentMaxOwnedUsers})
		}
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update("agent_id", agentID).Error
}

// ClearAgentAssignments releases every account the agent owns. The accounts
// themselves are left as they are.
func ClearAgentAssignments(tx *gorm.DB, agentID int) error {
	return tx.Unscoped().Model(&User{}).Where("agent_id = ?", agentID).Update("agent_id", 0).Error
}
