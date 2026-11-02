package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBulkUpdateReappliesOpenAIDailyQuotaBeforeCommit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		updates service.AccountBulkUpdate
	}{
		{"plan change", service.AccountBulkUpdate{Credentials: map[string]any{"plan_type": "plus"}}},
		{"daily limit override", service.AccountBulkUpdate{Extra: map[string]any{"quota_daily_limit": 1000}}},
		{"5h limit override", service.AccountBulkUpdate{Extra: map[string]any{"quota_5h_limit": 1000}}},
		{"policy marker override", service.AccountBulkUpdate{Extra: map[string]any{service.OpenAIDailyQuotaManagedKey: false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			mock.ExpectBegin()
			// Ordered expectations also check policy-before-row locking.
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_hour":2,"reset_timezone":"UTC","plan_limits":{"plus":20}}`))
			mock.ExpectExec(`(?s)UPDATE accounts SET .* WHERE id = ANY\(\$2\)`).WithArgs(sqlmock.AnyArg(), "{11,12}").WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*lower\(btrim\(credentials->>'plan_type'\)\).*platform = 'openai'.*parent_account_id IS NULL.*id = ANY\(\$7::bigint\[\]\) RETURNING id`).
				WithArgs(`{"plus":20}`, 0.0, 2, "UTC", sqlmock.AnyArg(), sqlmock.AnyArg(), "{11,12}", `{}`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
			mock.ExpectExec("INSERT INTO scheduler_outbox").
				WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{11, 12}}).
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			changed, err := repo.BulkUpdate(context.Background(), []int64{11, 12}, tc.updates)
			require.NoError(t, err)
			require.EqualValues(t, 2, changed)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBulkUpdateRollsBackWhenOpenAIDailyQuotaApplyFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{}}`))
	mock.ExpectExec(`UPDATE accounts SET credentials`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*id = ANY\(\$7::bigint\[\]\) RETURNING id`).WillReturnError(errors.New("quota apply failed"))
	mock.ExpectRollback()

	changed, err := repo.BulkUpdate(context.Background(), []int64{11}, service.AccountBulkUpdate{Credentials: map[string]any{"plan_type": "pro"}})
	require.EqualError(t, err, "quota apply failed")
	require.Zero(t, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateDisabledPolicyKeepsManualDailyLimits(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectExec(`UPDATE accounts SET extra`).WithArgs(sqlmock.AnyArg(), "{11}").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	changed, err := repo.BulkUpdate(context.Background(), []int64{11}, service.AccountBulkUpdate{Extra: map[string]any{"quota_daily_limit": 30}})
	require.NoError(t, err)
	require.EqualValues(t, 1, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIDailyQuotaEmptyTargetListDoesNotUpdatePool(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ids, err := applyOpenAIDailyQuotaPolicyToIDsSQL(context.Background(), db, service.DefaultOpenAIDailyQuotaPolicy(), nil)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}
