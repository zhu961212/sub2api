package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// EvaluateModel is a separate diagnostic endpoint: successful quality probes
// never invoke the connectivity test's account recovery workflow.
func (h *AccountHandler) EvaluateModel(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var req service.ModelEvaluationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid model evaluation request")
		return
	}
	if h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Model evaluation is unavailable")
		return
	}
	if err := h.accountTestService.EvaluateOpenAIAccount(c, accountID, req); err != nil && !c.Writer.Written() {
		response.ErrorFrom(c, err)
	}
}

func (h *AccountHandler) GetModelEvaluationSettings(c *gin.Context) {
	if h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Model evaluation is unavailable")
		return
	}
	settings, err := h.accountTestService.GetModelEvaluationAutoSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) UpdateModelEvaluationSettings(c *gin.Context) {
	if h.accountTestService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Model evaluation is unavailable")
		return
	}
	var settings service.ModelEvaluationAutoSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid model evaluation settings")
		return
	}
	updated, err := h.accountTestService.UpdateModelEvaluationAutoSettings(c.Request.Context(), settings)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}
