package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelEvaluationHistoryDoesNotInvalidateScheduling(t *testing.T) {
	require.False(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		service.ModelEvaluationResultsExtraKey: []any{},
	}))
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		service.ModelEvaluationResultsExtraKey: []any{},
		"quota_daily_limit":                    100,
	}))
}

func TestModelEvaluationHistorySurvivesStaleAccountEdits(t *testing.T) {
	fresh := []byte(`[{"id":"new-test","kind":"pelican","answer":"complete response","svg":"<svg><circle/></svg>","status":"completed"},{"id":"previous-test"}]`)
	freshPause := []byte(`{"result_id":"auto-wrong","reason":"candy_wrong"}`)
	for _, tc := range []struct {
		name    string
		input   any
		current []byte
	}{
		{"stale form", []any{map[string]any{"id": "previous-test"}}, fresh},
		{"lite list summary", []any{map[string]any{"id": "new-test", "answer": ""}}, fresh},
		{"missing field", nil, fresh},
		{"explicit clear", []any{}, fresh},
		{"forged history", []any{map[string]any{"id": "forged"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT")+`.*`+regexp.QuoteMeta("FOR NO KEY UPDATE")).
				WithArgs(int64(51), service.PlatformOpenAI, service.AccountTypeOAuth, `{"access_token":"token"}`, nil).
				WillReturnRows(sqlmock.NewRows(openCodeGoMergeMockColumns()).AddRow(
					false, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, nil, nil, tc.current, freshPause, false,
				))
			extra := map[string]any{"custom": "edited", service.ModelEvaluationPauseExtraKey: map[string]any{"reason": "stale-form"}}
			if tc.input != nil {
				extra[service.ModelEvaluationResultsExtraKey] = tc.input
			}
			account := &service.Account{ID: 51, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Schedulable: true,
				Credentials: map[string]any{"access_token": "token"}, Extra: extra}
			merged, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "edited", merged["custom"])
			require.False(t, account.Schedulable, "stale account editing must not resume an automatically paused account")
			require.Equal(t, map[string]any{"result_id": "auto-wrong", "reason": "candy_wrong"}, merged[service.ModelEvaluationPauseExtraKey])
			if tc.current == nil {
				require.NotContains(t, merged, service.ModelEvaluationResultsExtraKey)
			} else {
				var expected any
				require.NoError(t, json.Unmarshal(tc.current, &expected))
				require.Equal(t, expected, merged[service.ModelEvaluationResultsExtraKey])
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
