//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFiveHourQuotaCrossingPublishesSchedulerEvent(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "transactional", true: "legacy"}[legacy], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			ctx := context.Background()
			if !legacy {
				mock.ExpectBegin()
			}
			query := `(?s)UPDATE accounts SET extra.*quota_5h_used.*RETURNING`
			if !legacy {
				query = regexp.QuoteMeta(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(false, false)])
			}
			mock.ExpectQuery(query).WithArgs(0.5, int64(7)).WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(2, 0, 2, 20, 0, 0, 2, 2))
			mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			if legacy {
				require.NoError(t, newAccountRepositoryWithSQL(nil, db, nil).IncrementQuotaUsed(ctx, 7, 0.5))
			} else {
				mock.ExpectCommit()
				tx, err := db.BeginTx(ctx, nil)
				require.NoError(t, err)
				state, err := incrementUsageBillingAccountQuota(ctx, tx, 7, 0.5, false, false)
				require.NoError(t, err)
				require.Equal(t, 2.0, state.FiveHourUsed)
				require.Equal(t, 2.0, state.FiveHourLimit)
				require.NoError(t, tx.Commit())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountFiveHourQuotaResetClearsUsageAndPeriod(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(`(?s)UPDATE accounts SET extra.*"quota_5h_used": 0.*- 'quota_5h_start'`).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, newAccountRepositoryWithSQL(nil, db, nil).ResetQuotaUsedAndClearRateLimitCooldown(context.Background(), 7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountFiveHourQuotaFreeUsageDoesNotStartPeriod(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, newAccountRepositoryWithSQL(nil, db, nil).IncrementQuotaUsed(context.Background(), 7, 0))
	require.NoError(t, mock.ExpectationsWereMet(), "zero-cost usage must leave the first paid bill to start the period")
}
