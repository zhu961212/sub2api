package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerAccountCustomizationRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	accounts := h.Admin.Account
	admin.GET("/openai-daily-quota", accounts.GetOpenAIDailyQuota)
	admin.PUT("/openai-daily-quota", accounts.UpdateOpenAIDailyQuota)
	admin.GET("/accounts/model-evaluation-history", accounts.GetModelEvaluationHistory)
	admin.GET("/accounts/model-evaluation-settings", accounts.GetModelEvaluationSettings)
	admin.PUT("/accounts/model-evaluation-settings", accounts.UpdateModelEvaluationSettings)
	admin.POST("/accounts/:id/model-evaluation", accounts.EvaluateModel)
}
