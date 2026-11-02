//go:build unit

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

func TestSaveOpenAIDailyQuotaPolicyAtomicApply(t *testing.T) {
	for _, failApply := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit policy and budgets", true: "rollback policy if budgets fail"}[failApply], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			policy := service.DefaultOpenAIDailyQuotaPolicy()
			policy.Enabled = true
			policy.PlanLimits["plus"] = 20
			policy.Plan5hLimits["plus"] = 5
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			apply := mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*quota_5h_limit.*quota_daily_reset_timezone.*RETURNING id`).
				WithArgs(sqlmock.AnyArg(), 0.0, policy.ResetHour, policy.ResetTimezone, sqlmock.AnyArg(), sqlmock.AnyArg(), int64(0), `{"plus":5}`)
			if failApply {
				apply.WillReturnError(errors.New("db write failed"))
				mock.ExpectRollback()
			} else {
				apply.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11).AddRow(12))
				mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{11, 12}}).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectQuery(`(?s)SELECT .*COUNT\(\*\).*GROUP BY 1`).WillReturnRows(sqlmock.NewRows([]string{"plan", "count"}).AddRow("plus", 2))
				mock.ExpectCommit()
			}
			state, err := repo.SaveOpenAIDailyQuotaPolicy(context.Background(), policy)
			if failApply {
				require.Error(t, err)
				require.Nil(t, state)
			} else {
				require.NoError(t, err)
				require.Equal(t, 2, state.AppliedCount)
				require.Equal(t, 2, state.AccountCount)
				require.Equal(t, 5.0, state.Policy.Plan5hLimits["plus"])
				require.Contains(t, state.Plans, service.OpenAIDailyQuotaPlan{PlanType: "plus", AccountCount: 2})
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOpenAIDailyQuotaStateMaterializesOccupiedSKULimits(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	policy := service.OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC", PlanLimits: map[string]float64{
		"pro_100": 100, "pro_200": 200, "pro_500": 500, "prolite": 0, "enterprise": 25,
	}, Plan5hLimits: map[string]float64{"pro_100": 25, "pro_200": 50, "pro_500": 75, "prolite": 0, "five_hour_only": 5}}
	mock.ExpectQuery(`(?s)SELECT .*COUNT\(\*\).*GROUP BY 1`).WillReturnRows(sqlmock.NewRows([]string{"plan", "count"}).
		AddRow("prolite", 2).AddRow("chatgptpro", 1).AddRow("promax", 1).AddRow("pro", 3))
	state, err := openAIDailyQuotaState(context.Background(), db, policy)
	require.NoError(t, err)
	require.Equal(t, 7, state.AccountCount)
	require.Equal(t, 0.0, state.Policy.PlanLimits["prolite"], "explicit unlimited SKU override wins")
	require.Equal(t, 200.0, state.Policy.PlanLimits["chatgptpro"])
	require.Equal(t, 200.0, state.Policy.PlanLimits["pro"], "the raw Pro200 SKU materializes its matching tier for the editor")
	require.Equal(t, 500.0, state.Policy.PlanLimits["promax"])
	require.Equal(t, 25.0, state.Policy.PlanLimits["enterprise"], "unoccupied plan overrides remain intact")
	require.Equal(t, 0.0, state.Policy.Plan5hLimits["prolite"])
	require.Equal(t, 50.0, state.Policy.Plan5hLimits["chatgptpro"])
	require.Equal(t, 50.0, state.Policy.Plan5hLimits["pro"])
	require.Equal(t, 75.0, state.Policy.Plan5hLimits["promax"])
	require.Equal(t, 5.0, state.Policy.Plan5hLimits["five_hour_only"])
	require.Contains(t, state.Plans, service.OpenAIDailyQuotaPlan{PlanType: "five_hour_only"})
	require.NotContains(t, policy.PlanLimits, "chatgptpro", "materializing the editor response must not mutate the input policy")
	require.NotContains(t, policy.Plan5hLimits, "chatgptpro")
	require.NotContains(t, policy.PlanLimits, "pro")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyOpenAIDailyQuotaSQLIncludesExplicitSKUAndTierFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	policy := service.OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC", PlanLimits: map[string]float64{"pro_100": 75, "prolite": 0}, Plan5hLimits: map[string]float64{"pro_100": 25, "prolite": 0}}
	mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*COALESCE\(.*WHEN 'prolite' THEN 'pro_100'.*WHEN 'pro' THEN 'pro_200'.*WHEN 'chatgptpro' THEN 'pro_200'.*WHEN 'promax' THEN 'pro_500'.*RETURNING id`).
		WithArgs(`{"pro_100":75,"prolite":0}`, 0.0, 0, "UTC", sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), `{"pro_100":25,"prolite":0}`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	ids, err := applyOpenAIDailyQuotaPolicySQL(context.Background(), db, policy, 7)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIDailyQuotaStatePreservesLegacyPro200UnlimitedOverride(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	policy := service.OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC",
		PlanLimits: map[string]float64{"pro": 0, "pro_200": 75}, Plan5hLimits: map[string]float64{"pro": 0, "pro_200": 15}}
	mock.ExpectQuery(`(?s)SELECT .*COUNT\(\*\).*GROUP BY 1`).
		WillReturnRows(sqlmock.NewRows([]string{"plan", "count"}).AddRow("pro", 2))
	state, err := openAIDailyQuotaState(context.Background(), db, policy)
	require.NoError(t, err)
	require.Equal(t, 0.0, state.Policy.PlanLimits["pro"], "the editor must see the actual raw-SKU override")
	require.Equal(t, 0.0, state.Policy.Plan5hLimits["pro"])
	require.Equal(t, 75.0, state.Policy.PlanLimits["pro_200"], "the canonical preset remains intact")
	require.Equal(t, 15.0, state.Policy.Plan5hLimits["pro_200"])
	require.Contains(t, state.Plans, service.OpenAIDailyQuotaPlan{PlanType: "pro", AccountCount: 2})
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInheritOpenAIDailyQuotaPolicyPreservesCurrentCounters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_timezone":"Asia/Shanghai","plan_limits":{"plus":20},"plan_5h_limits":{"plus":5}}`))
	mock.ExpectQuery("SELECT extra FROM accounts").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"extra"}).AddRow(`{"quota_daily_used":15,"quota_daily_start":"2099-01-01T00:00:00Z","quota_used":44,"quota_5h_used":3,"quota_5h_start":"2099-01-01T00:00:00Z"}`))
	account := &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}, Extra: map[string]any{"quota_daily_used": 1.0, "quota_used": 2.0, "quota_5h_used": 1.0, "quota_5h_start": "2020-01-01T00:00:00Z"}}
	require.NoError(t, inheritOpenAIDailyQuotaPolicy(context.Background(), db, account))
	require.Equal(t, 20.0, account.GetQuotaDailyLimit())
	require.Equal(t, 15.0, account.GetQuotaDailyUsed())
	require.Equal(t, 44.0, account.GetQuotaUsed())
	require.Equal(t, 5.0, account.Extra["quota_5h_limit"])
	require.Equal(t, 3.0, account.Extra["quota_5h_used"])
	require.Equal(t, "2099-01-01T00:00:00Z", account.Extra["quota_5h_start"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffectsOpenAIOAuthQuota(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, false)])).
		WithArgs(2.5, int64(7)).WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(2.5, 0, 2.5, 20, 0, 0, 0, 0))
	mock.ExpectCommit()
	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(context.Background(), tx, &service.UsageBillingCommand{AccountID: 8, AccountQuotaAccountID: 7, AccountType: service.AccountTypeOAuth, AccountPlatform: service.PlatformOpenAI, AccountQuotaCost: 2.5, AccountQuotaHasDailyLimit: true}, result)
	require.NoError(t, err)
	require.NotNil(t, result.QuotaState)
	require.Equal(t, 2.5, result.QuotaState.DailyUsed)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLegacyAccountQuotaDailyCrossingPublishesSchedulerEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*RETURNING`).WithArgs(2.0, int64(7)).WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(20, 0, 11, 10, 0, 0, 0, 0))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	require.NoError(t, repo.IncrementQuotaUsed(context.Background(), 7, 2))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIDailyQuotaTokenRefreshReappliesPolicyWithoutPlanType(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock_shared(86420110)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE accounts.*credentials = \$1::jsonb`).WithArgs(`{"access_token":"new-token"}`, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOpenAIDailyQuotaPolicy).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":20}}`))
	mock.ExpectQuery(`(?s)UPDATE accounts SET extra.*quota_daily_reset_timezone.*RETURNING id`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.UpdateCredentials(context.Background(), 7, map[string]any{"access_token": "new-token"}))
	require.NoError(t, mock.ExpectationsWereMet())
}
