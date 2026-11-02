//go:build unit

package service

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestScopedAdminQuotaPolicyLockPrecedesAccountLocks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	admin := &adminServiceImpl{entClient: client}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`SELECT pg_advisory_xact_lock\(hashtextextended`).WithArgs(int64(31)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`SELECT id FROM accounts .* FOR UPDATE`).WithArgs(int64(31)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`SELECT account_id FROM account_groups .* FOR UPDATE`).WithArgs(int64(31)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	called := false
	account, err := admin.scopedAdminTransaction(context.Background(), 31, false, func(ctx context.Context) (*Account, error) {
		called = true
		require.NotNil(t, dbent.TxFromContext(ctx))
		return &Account{ID: 31}, nil
	})
	require.NoError(t, err)
	require.True(t, called)
	require.EqualValues(t, 31, account.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScopedAdminQuotaPolicyLockFailureStopsWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	admin := &adminServiceImpl{entClient: client}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnError(errors.New("policy lock failed"))
	mock.ExpectRollback()
	called := false
	account, err := admin.scopedAdminTransaction(context.Background(), 31, false, func(context.Context) (*Account, error) {
		called = true
		return &Account{ID: 31}, nil
	})
	require.EqualError(t, err, "policy lock failed")
	require.False(t, called)
	require.Nil(t, account)
	require.NoError(t, mock.ExpectationsWereMet())
}
