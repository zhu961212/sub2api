package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetModelEvaluationHistory returns a fresh batch projection without invoking
// account-detail enrichers or their ETag/full-account serialization paths.
func (h *AccountHandler) GetModelEvaluationHistory(c *gin.Context) {
	if adminaccess.FromContext(c.Request.Context()) != nil {
		response.Error(c, http.StatusForbidden, "Model evaluation history requires full administrator access")
		return
	}
	values := c.Request.URL.Query()["ids"]
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		response.BadRequest(c, "Provide a single ids query with 1 to 20 unique account IDs")
		return
	}
	parts := strings.Split(values[0], ",")
	if len(parts) > service.ModelEvaluationHistoryBatchLimit {
		response.BadRequest(c, "At most 20 account IDs may be requested")
		return
	}
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			response.BadRequest(c, "Account IDs must be positive integers")
			return
		}
		ids = append(ids, id)
	}
	if err := service.ValidateModelEvaluationHistoryIDs(ids); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	reader, ok := h.adminService.(service.ModelEvaluationHistoryReader)
	if !ok {
		response.Error(c, http.StatusServiceUnavailable, "Model evaluation history is unavailable")
		return
	}
	batch, err := reader.GetModelEvaluationHistory(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, batch)
}
