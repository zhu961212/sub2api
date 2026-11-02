package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIDailyQuotaPostgresSKUPresetsAndOverrides(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO accounts(id,credentials) VALUES
		(1,'{"plan_type":"prolite"}'), (2,'{"plan_type":"promax"}'),
		(3,'{"plan_type":"chatgptpro"}'), (4,'{"plan_type":"pro_100"}'),
		(5,'{"plan_type":"pro100"}'), (6,'{"plan_type":" Pro-200 "}'),
		(7,'{"plan_type":"pro"}'), (8,'{"plan_type":" PRO "}');`)
	require.NoError(t, err)
	policy := service.OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC", PlanLimits: map[string]float64{
		"pro_100": 30, "pro_200": 40, "pro_500": 50, "prolite": 0,
	}, Plan5hLimits: map[string]float64{"pro_100": 10, "pro_200": 15, "pro_500": 20, "prolite": 0}}
	ids, err := applyOpenAIDailyQuotaPolicySQL(ctx, db, policy, 0)
	require.NoError(t, err)
	require.Len(t, ids, 8)
	for id, expected := range map[int64]float64{1: 0, 2: 50, 3: 40, 4: 30, 5: 30, 6: 40, 7: 40, 8: 40} {
		var limit float64
		require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_daily_limit')::numeric FROM accounts WHERE id=$1`, id).Scan(&limit))
		require.Equal(t, expected, limit)
	}
	for id, expected := range map[int64]float64{1: 0, 2: 20, 3: 15, 4: 10, 5: 10, 6: 15, 7: 15, 8: 15} {
		var limit float64
		require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=$1`, id).Scan(&limit))
		require.Equal(t, expected, limit)
	}
	state, err := openAIDailyQuotaState(ctx, db, policy)
	require.NoError(t, err)
	require.Equal(t, 8, state.AccountCount)
	require.Equal(t, 0.0, state.Policy.PlanLimits["prolite"])
	require.Equal(t, 50.0, state.Policy.PlanLimits["promax"])
	require.Equal(t, 40.0, state.Policy.PlanLimits["chatgptpro"])
	require.Equal(t, 0.0, state.Policy.Plan5hLimits["prolite"])
	require.Equal(t, 20.0, state.Policy.Plan5hLimits["promax"])
	require.Equal(t, 15.0, state.Policy.Plan5hLimits["chatgptpro"])
	require.Equal(t, 40.0, state.Policy.PlanLimits["pro"])
	require.Equal(t, 15.0, state.Policy.Plan5hLimits["pro"])
	policy.PlanLimits["pro"] = 0
	policy.Plan5hLimits["pro"] = 0
	_, err = applyOpenAIDailyQuotaPolicySQL(ctx, db, policy, 0)
	require.NoError(t, err)
	var daily, fiveHour float64
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_daily_limit')::numeric,(extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=7`).Scan(&daily, &fiveHour))
	require.Zero(t, daily, "legacy raw Pro200 unlimited override must win over a nonzero tier limit")
	require.Zero(t, fiveHour)
}

func TestOpenAIDailyQuotaPostgresPreservesFiveHourLedgerAcrossPolicyChanges(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO accounts(id,credentials,extra) VALUES
		(1,'{"plan_type":"plus"}','{"quota_5h_used":3.25,"quota_5h_start":"2026-11-01T23:00:00Z","quota_daily_used":19,"quota_daily_start":"2020-01-01T00:00:00Z"}'),
		(2,'{"plan_type":"plus"}','{}'),
		(3,'{"plan_type":"plus"}','{"quota_5h_limit":99,"quota_5h_used":7,"quota_5h_start":"2026-11-01T23:00:00Z"}');`)
	require.NoError(t, err)
	policy := service.OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC", PlanLimits: map[string]float64{"plus": 20}, Plan5hLimits: map[string]float64{"plus": 5}}
	ids, err := applyOpenAIDailyQuotaPolicyToIDsSQL(ctx, db, policy, []int64{1, 2})
	require.NoError(t, err)
	require.Len(t, ids, 2)
	var dailyUsed, fiveHourUsed, limit float64
	var start string
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_daily_used')::numeric,(extra->>'quota_5h_used')::numeric,extra->>'quota_5h_start',(extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=1`).Scan(&dailyUsed, &fiveHourUsed, &start, &limit))
	require.Zero(t, dailyUsed)
	require.Equal(t, 3.25, fiveHourUsed)
	require.Equal(t, "2026-11-01T23:00:00Z", start)
	require.Equal(t, 5.0, limit)
	var started bool
	require.NoError(t, db.QueryRow(`SELECT extra ? 'quota_5h_start' FROM accounts WHERE id=2`).Scan(&started))
	require.False(t, started, "applying policy must not start the billing window")
	policy.ResetHour = 12
	policy.ResetTimezone = "Asia/Shanghai"
	policy.Plan5hLimits["plus"] = 8
	_, err = applyOpenAIDailyQuotaPolicyToIDsSQL(ctx, db, policy, []int64{1, 2})
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_used')::numeric,extra->>'quota_5h_start',(extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=1`).Scan(&fiveHourUsed, &start, &limit))
	require.Equal(t, 3.25, fiveHourUsed)
	require.Equal(t, "2026-11-01T23:00:00Z", start)
	require.Equal(t, 8.0, limit)
	policy.Enabled = false
	_, err = applyOpenAIDailyQuotaPolicySQL(ctx, db, policy, 0)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_used')::numeric,extra->>'quota_5h_start',extra ? 'quota_5h_limit' FROM accounts WHERE id=1`).Scan(&fiveHourUsed, &start, &started))
	require.Equal(t, 3.25, fiveHourUsed)
	require.Equal(t, "2026-11-01T23:00:00Z", start)
	require.False(t, started)
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=3`).Scan(&limit))
	require.Equal(t, 99.0, limit, "disabled policy must preserve unmanaged account budgets")
}
