package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelEvaluationLiteProjectionOnlyChangesListExtraColumn(t *testing.T) {
	for _, lite := range []bool{false, true} {
		t.Run(map[bool]string{false: "full read", true: "lite read"}[lite], func(t *testing.T) {
			var queries []string
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
				queries = append(queries, actual)
				return nil
			})))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			mock.ExpectQuery("count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectQuery("list").WillReturnRows(sqlmock.NewRows([]string{"id", "extra"}))
			ctx := context.Background()
			if lite {
				ctx = service.WithAccountListEvaluationSummaries(ctx)
			}
			_, _, err = repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, "", "", "", "", 0, "")
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			require.Len(t, queries, 2)
			require.NotContains(t, queries[0], "jsonb_array_elements", "Count must not evaluate answer histories")
			if lite {
				require.Contains(t, queries[1], "jsonb_array_elements")
				require.Contains(t, queries[1], "AS \"extra\"")
				require.Contains(t, queries[1], "- 'answer' - 'svg'")
				require.Contains(t, queries[1], "LIMIT 5")
			} else {
				require.NotContains(t, queries[1], "jsonb_array_elements")
			}
			for _, field := range dbaccount.Columns {
				if field != dbaccount.FieldExtra {
					require.Contains(t, queries[1], `"accounts"."`+field+`"`, "all other Ent columns must survive")
				}
			}
		})
	}
}

func TestModelEvaluationPostgresLiteExtraProjection(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	large := strings.Repeat("large-answer-", 30000)
	for _, test := range []struct {
		name    string
		history any
		ids     []string
	}{
		{"both projects", []any{
			map[string]any{"id": "pelican-latest", "kind": "pelican", "answer": large, "svg": "<svg>" + large + "</svg>", "status": "completed"},
			map[string]any{"id": "pelican-older", "kind": "pelican", "answer": large},
			map[string]any{"id": "candy-latest", "kind": "candy", "answer": "21", "status": "correct"},
		}, []string{"pelican-latest", "candy-latest"}},
		{"empty history", []any{}, nil},
		{"malformed history", map[string]any{"answer": large}, nil},
		{"malformed entries", []any{nil, 1, "bad", map[string]any{"id": "candy", "kind": "candy", "answer": "21"}}, []string{"candy"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			extra, err := json.Marshal(map[string]any{"model_evaluation_results": test.history, "quota_daily_limit": 30, "privacy_mode": "training_off"})
			require.NoError(t, err)
			var raw []byte
			require.NoError(t, db.QueryRow("SELECT "+accountListEvaluationExtraSQL("$1::jsonb"), string(extra)).Scan(&raw))
			require.Less(t, len(raw), 4096, "answer/SVG bodies must not cross the database boundary")
			var got struct {
				Quota   int                                `json:"quota_daily_limit"`
				Privacy string                             `json:"privacy_mode"`
				Results []struct{ ID, Answer, SVG string } `json:"model_evaluation_results"`
			}
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, 30, got.Quota)
			require.Equal(t, "training_off", got.Privacy)
			require.Len(t, got.Results, len(test.ids))
			for i, record := range got.Results {
				require.Equal(t, test.ids[i], record.ID)
				require.Empty(t, record.Answer)
				require.Empty(t, record.SVG)
			}
		})
	}
}
