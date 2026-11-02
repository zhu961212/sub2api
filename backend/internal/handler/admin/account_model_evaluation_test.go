package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerModelEvaluationRejectsInvalidInputBeforeStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &AccountHandler{accountTestService: &service.AccountTestService{}}
	router := gin.New()
	router.POST("/accounts/:id/model-evaluation", handler.EvaluateModel)
	for _, tc := range []struct {
		id   string
		body string
		code int
	}{
		{"bad", `{}`, http.StatusBadRequest},
		{"0", `{}`, http.StatusBadRequest},
		{"1", `{`, http.StatusBadRequest},
		{"1", `{"kind":"fingerprint"}`, http.StatusBadRequest},
		{"1", `{"runs":11}`, http.StatusBadRequest},
		{"1", `{"effort":"unknown"}`, http.StatusBadRequest},
		{"1", `{"kind":"pelican"}`, http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/accounts/"+tc.id+"/model-evaluation", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.code, recorder.Code, tc.body)
		require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
		require.NotContains(t, recorder.Body.String(), "test_complete")
	}
}

func TestAccountHandlerModelEvaluationSettingsDefaultsAndValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &AccountHandler{accountTestService: &service.AccountTestService{}}
	router := gin.New()
	router.GET("/settings", handler.GetModelEvaluationSettings)
	router.PUT("/settings", handler.UpdateModelEvaluationSettings)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"enabled":false`)
	require.Contains(t, recorder.Body.String(), `"interval_hours":12`)
	require.Contains(t, recorder.Body.String(), `"concurrency":3`)
	for _, body := range []string{
		`{"enabled":true,"interval_hours":0,"concurrency":3,"effort":"low"}`,
		`{"enabled":true,"interval_hours":12,"concurrency":11,"effort":"low"}`,
		`{"enabled":true,"interval_hours":12,"concurrency":3,"effort":"invalid"}`,
	} {
		recorder = httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, body)
	}
}
