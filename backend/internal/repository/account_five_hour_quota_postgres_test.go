package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountQuotaPostgresFixedDailyStartAndResetMetadataStayConsistent(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	now := time.Now()
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	// Keep the boundary away from test execution so both sides remain stable.
	policy := service.OpenAIDailyQuotaPolicy{ResetTimezone: "Asia/Shanghai", ResetHour: (now.In(location).Hour() + 12) % 24}
	last, next := policy.WindowBounds(now)
	for _, legacy := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			start     string
			resetAt   time.Time
			wantDaily float64
		}{
			{name: "old_start_future_reset", start: last.Add(-time.Hour).Format(time.RFC3339Nano), resetAt: now.Add(7 * 24 * time.Hour), wantDaily: 0.32},
			{name: "missing_start_future_reset", resetAt: now.Add(7 * 24 * time.Hour), wantDaily: 0.32},
			{name: "active_start_stale_reset", start: now.Add(-time.Minute).Format(time.RFC3339Nano), resetAt: now.Add(-48 * time.Hour), wantDaily: 9.32},
			{name: "active_start_future_reset", start: now.Add(-time.Minute).Format(time.RFC3339Nano), resetAt: next, wantDaily: 9.32},
		} {
			path := "transactional/"
			if legacy {
				path = "legacy/"
			}
			t.Run(path+tc.name, func(t *testing.T) {
				extra := map[string]any{
					"quota_5h_limit": 200, "quota_5h_used": 1.25, "quota_5h_start": now.Add(-time.Hour).Format(time.RFC3339Nano),
					"quota_daily_limit": 400, "quota_daily_used": 9, "quota_daily_reset_mode": "fixed",
					"quota_daily_reset_hour": policy.ResetHour, "quota_daily_reset_timezone": policy.ResetTimezone,
					"quota_reset_timezone": "America/New_York", "quota_daily_reset_at": tc.resetAt.Format(time.RFC3339Nano),
				}
				if tc.start != "" {
					extra["quota_daily_start"] = tc.start
				}
				payload, err := json.Marshal(extra)
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO accounts(id,extra) VALUES(1,$1::jsonb) ON CONFLICT(id) DO UPDATE SET extra=EXCLUDED.extra`, string(payload))
				require.NoError(t, err)
				repo := newAccountRepositoryWithSQL(nil, db, nil)
				increment := func(amount float64) {
					if legacy {
						require.NoError(t, repo.IncrementQuotaUsed(ctx, 1, amount))
						return
					}
					tx, err := db.BeginTx(ctx, nil)
					require.NoError(t, err)
					defer func() { _ = tx.Rollback() }()
					// Stale scheduling hints must still use the full database config.
					_, err = incrementUsageBillingAccountQuota(ctx, tx, 1, amount, false, false)
					require.NoError(t, err)
					require.NoError(t, tx.Commit())
				}
				readAccount := func() *service.Account {
					var raw []byte
					require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=1`).Scan(&raw))
					var stored map[string]any
					require.NoError(t, json.Unmarshal(raw, &stored))
					return &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: stored}
				}
				increment(0.32)
				account := readAccount()
				require.InDelta(t, tc.wantDaily, account.GetQuotaDailyUsed(), 0.000001)
				require.InDelta(t, 1.57, account.GetQuota5hUsed(), 0.000001, "daily repair must not reset the active five-hour ledger")
				require.False(t, account.IsDailyQuotaPeriodExpired(), "billing and API mapping must agree on the active day")
				view := dto.AccountFromServiceShallow(account)
				require.InDelta(t, tc.wantDaily, *view.QuotaDailyUsed, 0.000001, "the account API must expose the actual daily usage")
				reset, err := time.Parse(time.RFC3339, *view.QuotaDailyResetAt)
				require.NoError(t, err)
				require.WithinDuration(t, next, reset, time.Second)
				increment(0.43)
				account = readAccount()
				require.InDelta(t, tc.wantDaily+0.43, account.GetQuotaDailyUsed(), 0.000001, "later bills continue accumulating within the repaired day")
				require.InDelta(t, 2.0, account.GetQuota5hUsed(), 0.000001)
			})
		}
	}
}

// Uses the same opt-in isolated PostgreSQL schema as model evaluation tests.
func TestAccountFiveHourQuotaPostgresAtomicPeriodsAndReset(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	_, err := db.Exec(`ALTER TABLE accounts ADD COLUMN rate_limited_at TIMESTAMPTZ;
		INSERT INTO accounts(id,extra) VALUES(1,'{"quota_5h_limit":2,"quota_daily_limit":20,"quota_weekly_limit":100,"unrelated":"keep"}');`)
	require.NoError(t, err)
	increment := func(amount float64) (*service.AccountQuotaState, error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		state, err := incrementUsageBillingAccountQuota(ctx, tx, 1, amount, false, false)
		if err != nil {
			return nil, err
		}
		return state, tx.Commit()
	}
	state, err := increment(1.25)
	require.NoError(t, err)
	require.Equal(t, 1.25, state.FiveHourUsed)
	var firstStart, dailyStart string
	require.NoError(t, db.QueryRow(`SELECT extra->>'quota_5h_start',extra->>'quota_daily_start' FROM accounts WHERE id=1`).Scan(&firstStart, &dailyStart))
	start, err := time.Parse(time.RFC3339Nano, firstStart)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), start, time.Second)
	state, err = increment(0.75)
	require.NoError(t, err)
	require.Equal(t, 2.0, state.FiveHourUsed)
	var preserved string
	require.NoError(t, db.QueryRow(`SELECT extra->>'quota_5h_start' FROM accounts WHERE id=1`).Scan(&preserved))
	require.Equal(t, firstStart, preserved, "a second bill must not slide the anchored window")
	var events int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM scheduler_outbox`).Scan(&events))
	require.Equal(t, 1, events, "only the five-hour threshold was crossed")
	// Match the production consumer: release pending-event deduplication after
	// observing a change so later independent crossings publish a new event.
	_, err = db.Exec(`UPDATE scheduler_outbox SET dedup_key=NULL`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_set(extra,'{quota_5h_start}',to_jsonb((NOW()-INTERVAL '5 hours')::text)) WHERE id=1`)
	require.NoError(t, err)
	state, err = increment(0.5)
	require.NoError(t, err)
	require.Equal(t, 0.5, state.FiveHourUsed, "the first bill after five hours starts a new period")
	require.Equal(t, 2.5, state.DailyUsed, "the daily budget does not reset with the shorter period")
	require.NoError(t, db.QueryRow(`SELECT extra->>'quota_daily_start' FROM accounts WHERE id=1`).Scan(&preserved))
	require.Equal(t, dailyStart, preserved)
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	require.NoError(t, repo.IncrementQuotaUsed(ctx, 1, 0.25))
	var used float64
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_used')::numeric FROM accounts WHERE id=1`).Scan(&used))
	require.Equal(t, 0.75, used, "legacy billing uses the same account period")
	require.NoError(t, repo.ResetQuotaUsedAndClearRateLimitCooldown(ctx, 1))
	_, err = db.Exec(`UPDATE scheduler_outbox SET dedup_key=NULL`)
	require.NoError(t, err)
	require.NoError(t, repo.IncrementQuotaUsed(ctx, 1, 0))
	var hasStart bool
	var limit float64
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_used')::numeric,extra?'quota_5h_start',(extra->>'quota_5h_limit')::numeric FROM accounts WHERE id=1`).Scan(&used, &hasStart, &limit))
	require.Zero(t, used)
	require.False(t, hasStart)
	require.Equal(t, 2.0, limit, "manual usage reset preserves configured limits")
	var wg sync.WaitGroup
	errors := make(chan error, 24)
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := increment(0.25); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_5h_used')::numeric FROM accounts WHERE id=1`).Scan(&used))
	require.Equal(t, 6.0, used, "simultaneous bills must not lose increments")
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM scheduler_outbox`).Scan(&events))
	require.Equal(t, 3, events, "one threshold event per five-hour crossing plus the explicit reset")
}
