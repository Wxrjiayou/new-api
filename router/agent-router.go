package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// registerAgentRoutes mounts the read-only agent API under its own /agent
// namespace. Every handler checks the caller's role itself.
func registerAgentRoutes(apiRouter *gin.RouterGroup) {
	agentRoute := apiRouter.Group("/agent")
	agentRoute.Use(middleware.UserAuth())
	{
		agentRoute.GET("/users", controller.GetAgentUsers)
		agentRoute.GET("/users/:id", controller.GetAgentUser)
		agentRoute.GET("/roster", controller.GetAgentRoster)
	}
}
