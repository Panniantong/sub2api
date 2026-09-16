package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

// All routes inherit authentication, hierarchy checks and the administrative audit middleware.
func registerRelayManagementRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	cleanup := admin.Group("/users/cleanup")
	cleanup.GET("/summary", h.Admin.UserCleanup.Summary)
	cleanup.POST("/preview", h.Admin.UserCleanup.Preview)
	cleanup.POST("/execute", h.Admin.UserCleanup.Execute)
	admin.GET("/users/:id/cleanup-guard", h.Admin.UserCleanup.GetGuard)
	admin.PUT("/users/:id/cleanup-guard", h.Admin.UserCleanup.UpdateGuard)
	admin.POST("/accounts/:id/protection", h.Admin.AntiDegrade.SetProtection)
	admin.GET("/accounts/:id/traffic-control", h.Admin.AccountTraffic.Get)
	admin.PUT("/accounts/:id/traffic-control", h.Admin.AccountTraffic.Update)
	admin.POST("/accounts/protection/enable-batch", h.Admin.AntiDegrade.EnableBatch)
	tests := admin.Group("/intelligent-tests")
	tests.GET("/accounts", h.Admin.IntelligentTest.Accounts)
	tests.GET("/records", h.Admin.IntelligentTest.Records)
	tests.GET("/records/:id", h.Admin.IntelligentTest.Get)
	tests.GET("/records/:id/image", h.Admin.IntelligentTest.Image)
	tests.POST("/records/:id/cancel", h.Admin.IntelligentTest.Cancel)
	tests.POST("/records/:id/reevaluate", h.Admin.IntelligentTest.Reevaluate)
	tests.POST("/evaluate-preview", h.Admin.IntelligentTest.PreviewEvaluation)
	tests.POST("/run", h.Admin.IntelligentTest.Run)
	tests.GET("/settings", h.Admin.IntelligentTest.Settings)
	tests.PUT("/settings/:test_type", h.Admin.IntelligentTest.UpdateSetting)
}

func registerAccountCapabilityRoutes(authenticated *gin.RouterGroup, h *handler.Handlers) {
	capabilities := authenticated.Group("/account-capabilities")
	capabilities.GET("", h.AccountCapability.List)
	capabilities.GET("/:account_id/tests", h.AccountCapability.History)
	capabilities.GET("/results/:id", h.AccountCapability.Get)
	capabilities.GET("/results/:id/image", h.AccountCapability.Image)
}
