//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountCustomizationRoutesStayBehindAdminMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	registerAccountCustomizationRoutes(admin, &handler.Handlers{
		Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}},
	})
	routes := router.Routes()
	require.Len(t, routes, 6)
	for _, route := range routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			path := route.Path
			if route.Method == http.MethodPost {
				path = "/api/v1/admin/accounts/42/model-evaluation"
			}
			result := httptest.NewRecorder()
			router.ServeHTTP(result, httptest.NewRequest(route.Method, path, nil))
			require.Equal(t, http.StatusUnauthorized, result.Code)
			require.False(t, adminaccess.RouteAllowed(adminaccess.Policy{}, route.Method, route.Path))
			require.True(t, adminaccess.RouteAllowed(adminaccess.Policy{FullAccess: true}, route.Method, route.Path))
		})
	}
}
