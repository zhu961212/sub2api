package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountProjectionAdminService struct {
	service.AdminService
	summariesRequested bool
}

func (s *accountProjectionAdminService) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode, sortBy, sortOrder string) ([]service.Account, int64, error) {
	s.summariesRequested = service.AccountListEvaluationSummariesRequested(ctx)
	return []service.Account{}, 0, nil
}

func TestAccountHandlerOnlyLiteListsRequestDatabaseEvaluationSummaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &accountProjectionAdminService{}
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/accounts", handler.List)
	for _, test := range []struct {
		path string
		want bool
	}{{"/accounts?lite=1", true}, {"/accounts", false}, {"/accounts?lite=0", false}} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, test.want, svc.summariesRequested)
	}
}
