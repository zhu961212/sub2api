package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelEvaluationHistoryBatchUsesOneSafeProjectionAndReturnsPartialRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT a.id, a.name, a.platform").WithArgs("{9,5,77}").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "type", "schedulable", "proxy_id", "proxy_name", "results"}).
		AddRow(5, "broken history", "openai", "oauth", true, int64(12), nil, []byte(`{"unexpected":"object"}`)).
		AddRow(9, "ready", "openai", "oauth", false, nil, nil, []byte(`[{"id":"saved","kind":"candy","status":"correct","answer":"21","model":"gpt-test","effort":"low","tested_at":"2026-11-01T00:00:00Z","credentials":{"access_token":"SECRET"},"password":"PASSWORD"}]`)))
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	batch, err := repo.GetModelEvaluationHistory(context.Background(), []int64{9, 5, 77})
	require.NoError(t, err)
	require.Len(t, batch.Accounts, 2)
	require.EqualValues(t, 9, batch.Accounts[0].ID)
	require.Equal(t, "21", batch.Accounts[0].Results[0].Answer)
	require.Equal(t, "invalid_history", batch.Accounts[1].HistoryError)
	require.Empty(t, batch.Accounts[1].Results)
	require.EqualValues(t, 12, *batch.Accounts[1].ProxyID)
	require.Nil(t, batch.Accounts[1].ProxyName, "missing proxy relation is unknown, not a direct connection")
	require.Equal(t, []int64{77}, batch.MissingIDs)
	raw, err := json.Marshal(batch)
	require.NoError(t, err)
	for _, sensitive := range []string{"SECRET", "PASSWORD", "credentials", "group_ids", "extra"} {
		require.NotContains(t, string(raw), sensitive)
	}
	for _, sensitive := range []string{"credentials", "username", "password", "account_groups"} {
		require.NotContains(t, modelEvaluationHistoryQuery, sensitive)
	}
	require.Contains(t, modelEvaluationHistoryQuery, "LEFT JOIN proxies")
	require.Contains(t, modelEvaluationHistoryQuery, "ELSE parent.proxy_id")
	require.Contains(t, modelEvaluationHistoryQuery, "a.deleted_at IS NULL")
	require.Contains(t, modelEvaluationHistoryQuery, "parent.id IS NOT NULL")
	require.Contains(t, modelEvaluationHistoryQuery, "LIMIT 5")
	require.NoError(t, mock.ExpectationsWereMet(), "the history endpoint must issue one query, without detail enrichers")
}

func TestModelEvaluationHistoryReadBoundsAndInvalidEntries(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("null"), []byte("[]")} {
		results, historyError := decodeModelEvaluationHistory(raw)
		require.NotNil(t, results)
		require.Empty(t, results)
		require.Empty(t, historyError)
	}
	results, historyError := decodeModelEvaluationHistory([]byte(`[{"id":"bad","duration_ms":"bad-type"},{"id":"sparse"},{"id":"valid","kind":"candy","status":"wrong","model":"gpt-test","effort":"low","tested_at":"2026-11-01T00:00:00Z"}]`))
	require.Equal(t, "invalid_history", historyError)
	require.Len(t, results, 1)
	require.Equal(t, "valid", results[0].ID)
	history := make([]service.ModelEvaluationResult, 7)
	for i := range history {
		history[i] = service.ModelEvaluationResult{ID: "result", Kind: "pelican", Status: "completed", Model: "gpt-test", Effort: "low", TestedAt: "2026-11-01T00:00:00Z", Answer: strings.Repeat("x", 700000), SVG: "<svg/>"}
	}
	raw, err := json.Marshal(history)
	require.NoError(t, err)
	results, historyError = decodeModelEvaluationHistory(raw)
	require.Empty(t, historyError)
	require.Len(t, results, 1, "older large entries must obey the existing soft byte budget")
	require.Len(t, results[0].Answer, 700000, "newest result remains complete")
	for i := range history {
		history[i].Answer = "21"
	}
	raw, err = json.Marshal(history)
	require.NoError(t, err)
	results, historyError = decodeModelEvaluationHistory(raw)
	require.Empty(t, historyError)
	require.Len(t, results, 5)
}

func TestModelEvaluationHistoryDatabaseFailureIsNotDisguisedAsMissingAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT a.id").WillReturnError(errors.New("query unavailable"))
	batch, err := newAccountRepositoryWithSQL(nil, db, nil).GetModelEvaluationHistory(context.Background(), []int64{1})
	require.Error(t, err)
	require.Nil(t, batch)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationHistoryRepositoryRejectsInvalidIDsBeforeQuery(t *testing.T) {
	repo := newAccountRepositoryWithSQL(nil, nil, nil)
	for _, ids := range [][]int64{nil, {0}, {-1}, {1, 1}, make([]int64, 21)} {
		_, err := repo.GetModelEvaluationHistory(context.Background(), ids)
		require.Error(t, err)
	}
}
