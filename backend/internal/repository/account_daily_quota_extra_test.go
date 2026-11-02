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

func TestCreateOpenAIDailyQuotaInExistingEntTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":20},"plan_5h_limits":{"plus":5}}`))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO "accounts"`)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
	account := &service.Account{Name: "parent", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Credentials: map[string]any{"plan_type": "plus"}}

	// A transactional repository may be used without adding the transaction
	// to ctx. Create must inherit the policy without nesting or committing it.
	require.NoError(t, repo.Create(context.Background(), account))
	require.EqualValues(t, 7, account.ID)
	require.Equal(t, 20.0, account.GetQuotaDailyLimit())
	require.Equal(t, 5.0, account.Extra["quota_5h_limit"])
	require.NotContains(t, account.Extra, "quota_5h_start")
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateExtraReappliesOpenAIDailyQuotaBeforeCommit(t *testing.T) {
	for _, updates := range []map[string]any{
		{"quota_daily_limit": 1000},
		{"quota_5h_limit": 1000},
		{"quota_daily_reset_timezone": "America/New_York"},
		{service.OpenAIDailyQuotaManagedKey: false},
	} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
		repo := newAccountRepositoryWithSQL(client, db, nil)
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_hour":2,"reset_timezone":"UTC","plan_limits":{"plus":20}}`))
		mock.ExpectExec(`UPDATE accounts SET extra`).WithArgs(sqlmock.AnyArg(), int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*quota_daily_reset_timezone.*RETURNING id`).
			WithArgs(`{"plus":20}`, 0.0, 2, "UTC", sqlmock.AnyArg(), sqlmock.AnyArg(), int64(11), `{}`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
		mock.ExpectExec("INSERT INTO scheduler_outbox").
			WithArgs(service.SchedulerOutboxEventAccountChanged, int64(11), nil, nil, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		require.NoError(t, repo.UpdateExtra(context.Background(), 11, updates))
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestUpdateExtraRollsBackWhenOpenAIDailyQuotaApplyFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{}}`))
	mock.ExpectExec(`UPDATE accounts SET extra`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*RETURNING id`).WillReturnError(errors.New("quota apply failed"))
	mock.ExpectRollback()

	require.EqualError(t, repo.UpdateExtra(context.Background(), 11, map[string]any{"quota_daily_limit": 1000}), "quota apply failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateExtraDisabledPolicyKeepsManualDailyLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).
		WillReturnRows(sqlmock.NewRows([]string{"value"}))
	mock.ExpectExec(`UPDATE accounts SET extra`).WithArgs(`{"quota_daily_limit":30}`, int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateExtra(context.Background(), 11, map[string]any{"quota_daily_limit": 30}))
	require.NoError(t, mock.ExpectationsWereMet())
}
