package service

import (
	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

// AgentScopeFromContext returns the caller's id when the caller is an agent,
// whose usage views cover the accounts it owns as well as itself.
func AgentScopeFromContext(c *gin.Context) (int, bool) {
	if c.GetInt("role") != common.RoleAgentUser {
		return 0, false
	}
	return c.GetInt("id"), true
}
