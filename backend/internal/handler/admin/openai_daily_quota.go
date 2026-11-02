package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) GetOpenAIDailyQuota(c *gin.Context) {
	admin, ok := h.adminService.(service.OpenAIDailyQuotaPolicyAdmin)
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "Daily quota policy service unavailable")
		return
	}
	state, err := admin.GetOpenAIDailyQuotaState(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) UpdateOpenAIDailyQuota(c *gin.Context) {
	var policy service.OpenAIDailyQuotaPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		response.BadRequest(c, "Invalid daily quota policy")
		return
	}
	if err := policy.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	admin, ok := h.adminService.(service.OpenAIDailyQuotaPolicyAdmin)
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "Daily quota policy service unavailable")
		return
	}
	state, err := admin.SaveOpenAIDailyQuotaPolicy(c.Request.Context(), policy)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}
