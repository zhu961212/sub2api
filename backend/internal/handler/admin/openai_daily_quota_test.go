package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dailyQuotaAdminStub struct {
	service.AdminService
	policy service.OpenAIDailyQuotaPolicy
	saves  int
}

func (s *dailyQuotaAdminStub) GetOpenAIDailyQuotaState(context.Context) (*service.OpenAIDailyQuotaState, error) {
	return &service.OpenAIDailyQuotaState{Policy: s.policy, Plans: []service.OpenAIDailyQuotaPlan{{PlanType: "plus", AccountCount: 2}}, AccountCount: 2}, nil
}

func (s *dailyQuotaAdminStub) SaveOpenAIDailyQuotaPolicy(_ context.Context, policy service.OpenAIDailyQuotaPolicy) (*service.OpenAIDailyQuotaState, error) {
	s.policy = policy
	s.saves++
	return &service.OpenAIDailyQuotaState{Policy: policy, AppliedCount: 2, AccountCount: 2}, nil
}

func TestOpenAIDailyQuotaPolicyHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &dailyQuotaAdminStub{policy: service.DefaultOpenAIDailyQuotaPolicy()}
	h := &AccountHandler{adminService: stub}
	router := gin.New()
	router.GET("/quota", h.GetOpenAIDailyQuota)
	router.PUT("/quota", h.UpdateOpenAIDailyQuota)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/quota", strings.NewReader(`{"enabled":true,"reset_hour":0,"reset_timezone":"Asia/Shanghai","plan_limits":{"plus":20,"free":0},"plan_5h_limits":{"plus":5,"free":0}}`)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, stub.saves)
	require.Equal(t, 20.0, stub.policy.PlanLimits["plus"])
	require.Equal(t, 5.0, stub.policy.Plan5hLimits["plus"])
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/quota", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Data service.OpenAIDailyQuotaState `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 2, response.Data.AccountCount)
	require.True(t, response.Data.Policy.Enabled)
	require.Equal(t, 5.0, response.Data.Policy.Plan5hLimits["plus"])
}

func TestOpenAIDailyQuotaPolicyHandlerRejectsInvalidConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"enabled":true,"plan_limits":{"plus":-1},"reset_timezone":"UTC"}`,
		`{"enabled":true,"reset_hour":24,"reset_timezone":"UTC"}`,
		`{"enabled":true,"reset_timezone":"invalid"}`,
		`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":-1}}`,
		`{"enabled":true,"reset_timezone":"UTC","plan_5h_limits":{"plus":-1}}`,
		`{"enabled":true,"reset_timezone":"UTC","plan_5h_limits":{"Plus":5}}`,
	} {
		stub := &dailyQuotaAdminStub{}
		h := &AccountHandler{adminService: stub}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPut, "/quota", strings.NewReader(body))
		h.UpdateOpenAIDailyQuota(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Zero(t, stub.saves)
	}
}

func TestOpenAIDailyQuotaPolicyHandlerAcceptsLegacyRequestWithoutFiveHourLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &dailyQuotaAdminStub{}
	h := &AccountHandler{adminService: stub}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/quota", strings.NewReader(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":20}}`))
	h.UpdateOpenAIDailyQuota(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, stub.saves)
	require.NotNil(t, stub.policy.Plan5hLimits)
	require.Zero(t, stub.policy.FiveHourLimit("plus"))
	require.Contains(t, w.Body.String(), `"plan_5h_limits":{}`)
}
