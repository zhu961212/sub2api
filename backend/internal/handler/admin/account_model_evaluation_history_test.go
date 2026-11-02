package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type evaluationHistoryAdminStub struct {
	service.AdminService
	calls int
	ids   []int64
	err   error
}

func (s *evaluationHistoryAdminStub) GetModelEvaluationHistory(_ context.Context, ids []int64) (*service.ModelEvaluationHistoryBatch, error) {
	s.calls++
	s.ids = append([]int64(nil), ids...)
	if s.err != nil {
		return nil, s.err
	}
	return &service.ModelEvaluationHistoryBatch{
		Accounts:   []service.ModelEvaluationHistoryAccount{{ID: ids[0], Name: "account", Platform: "openai", Type: "oauth", Schedulable: true, Results: []service.ModelEvaluationResult{}, HistoryError: "invalid_history"}},
		MissingIDs: ids[1:],
	}, nil
}

func TestModelEvaluationHistoryHandlerReturnsBatchWithoutDetailOrETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &evaluationHistoryAdminStub{}
	h := &AccountHandler{adminService: stub}
	router := gin.New()
	router.GET("/history", h.GetModelEvaluationHistory)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/history?ids=2,8", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, stub.calls)
	require.Equal(t, []int64{2, 8}, stub.ids)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Empty(t, w.Header().Get("ETag"))
	var envelope struct {
		Data service.ModelEvaluationHistoryBatch `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Equal(t, []int64{8}, envelope.Data.MissingIDs)
	require.Equal(t, "invalid_history", envelope.Data.Accounts[0].HistoryError)
	for _, key := range []string{"credentials", "password", "group_ids", "extra"} {
		require.NotContains(t, w.Body.String(), key)
	}
}

func TestModelEvaluationHistoryHandlerRejectsInvalidIDListsBeforeReading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = strconv.Itoa(i + 1)
	}
	for _, query := range []string{"", "?ids=", "?ids=1,,2", "?ids=-1", "?ids=0", "?ids=1,1", "?ids=nope", "?ids=9223372036854775808", "?ids=1&ids=2", "?ids=" + strings.Join(tooMany, ",")} {
		stub := &evaluationHistoryAdminStub{}
		h := &AccountHandler{adminService: stub}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/history"+query, nil)
		h.GetModelEvaluationHistory(c)
		require.Equal(t, http.StatusBadRequest, w.Code, query)
		require.Zero(t, stub.calls, query)
	}
}

func TestModelEvaluationHistoryHandlerRejectsScopedAdminKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &evaluationHistoryAdminStub{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodGet, "/history?ids=1", nil)
	c.Request = request.WithContext(adminaccess.WithPrincipal(request.Context(), &adminaccess.Principal{KeyID: "restricted"}))
	(&AccountHandler{adminService: stub}).GetModelEvaluationHistory(c)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, stub.calls)
}

func TestModelEvaluationHistoryHandlerPreservesWholeRequestFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &evaluationHistoryAdminStub{err: errors.New("history database unavailable")}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/history?ids=1", nil)
	(&AccountHandler{adminService: stub}).GetModelEvaluationHistory(c)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "missing_ids")
}
