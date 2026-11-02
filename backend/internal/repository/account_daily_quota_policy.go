package repository

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const openAIDailyQuotaEligibleSQL = `platform = 'openai' AND type = 'oauth' AND parent_account_id IS NULL AND deleted_at IS NULL`
const openAIDailyQuotaRawPlanSQL = `COALESCE(NULLIF(lower(btrim(credentials->>'plan_type')), ''), 'unknown')`
const openAIDailyQuotaPlanSQL = `(CASE WHEN ` + openAIDailyQuotaRawPlanSQL + ` ~ '^pro[-_]?(100|200|500)$' THEN regexp_replace(` + openAIDailyQuotaRawPlanSQL + `, '^pro[-_]?', 'pro_') ELSE ` + openAIDailyQuotaRawPlanSQL + ` END)`
const openAIDailyQuotaFallbackPlanSQL = `(CASE ` + openAIDailyQuotaPlanSQL + ` WHEN 'prolite' THEN 'pro_100' WHEN 'pro' THEN 'pro_200' WHEN 'chatgptpro' THEN 'pro_200' WHEN 'promax' THEN 'pro_500' ELSE ` + openAIDailyQuotaPlanSQL + ` END)`

func inheritOpenAIDailyQuotaPolicy(ctx context.Context, db sqlExecutor, account *service.Account) error {
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() {
		return nil
	}
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(86420110)`); err != nil {
		return err
	}
	policy, err := readOpenAIDailyQuotaPolicy(ctx, db)
	if err != nil {
		return err
	}
	if account.ID > 0 {
		// updateLockedAccount already holds this row lock. Preserve counters from
		// that row instead of an earlier form/import snapshot.
		rows, err := db.QueryContext(ctx, `SELECT extra FROM accounts WHERE id = $1 AND deleted_at IS NULL`, account.ID)
		if err != nil {
			return err
		}
		var raw []byte
		if rows.Next() {
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var current map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
		}
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		for _, key := range []string{"quota_used", "quota_daily_used", "quota_daily_start", "quota_weekly_used", "quota_weekly_start", "quota_5h_used", "quota_5h_start"} {
			delete(account.Extra, key)
			if value, ok := current[key]; ok {
				account.Extra[key] = value
			}
		}
	}
	policy.ApplyToAccount(account, time.Now())
	return nil
}

func readOpenAIDailyQuotaPolicy(ctx context.Context, db sqlExecutor) (service.OpenAIDailyQuotaPolicy, error) {
	rows, err := db.QueryContext(ctx, `SELECT value FROM settings WHERE key = $1`, service.SettingKeyOpenAIDailyQuotaPolicy)
	if err != nil {
		return service.OpenAIDailyQuotaPolicy{}, err
	}
	defer func() { _ = rows.Close() }()
	var raw string
	if rows.Next() {
		if err := rows.Scan(&raw); err != nil {
			return service.OpenAIDailyQuotaPolicy{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return service.OpenAIDailyQuotaPolicy{}, err
	}
	return service.DecodeOpenAIDailyQuotaPolicy(raw)
}

func openAIDailyQuotaState(ctx context.Context, db sqlExecutor, policy service.OpenAIDailyQuotaPolicy) (*service.OpenAIDailyQuotaState, error) {
	state := &service.OpenAIDailyQuotaState{Policy: policy}
	// Materialize occupied SKU fallbacks for the editor without changing the
	// caller's policy or losing hidden overrides. Otherwise its missing field
	// would display zero even though the matching tier is enforced by the DB.
	state.Policy.PlanLimits = make(map[string]float64, len(policy.PlanLimits))
	for plan, limit := range policy.PlanLimits {
		state.Policy.PlanLimits[plan] = limit
	}
	state.Policy.Plan5hLimits = make(map[string]float64, len(policy.Plan5hLimits))
	for plan, limit := range policy.Plan5hLimits {
		state.Policy.Plan5hLimits[plan] = limit
	}
	counts := map[string]int{}
	for _, plan := range []string{"free", "plus", "pro", "pro_100", "pro_200", "pro_500", "team", "business", "enterprise", "edu", "unknown"} {
		counts[plan] = 0
	}
	for plan := range policy.PlanLimits {
		counts[plan] = 0
	}
	for plan := range policy.Plan5hLimits {
		counts[plan] = 0
	}
	rows, err := db.QueryContext(ctx, `SELECT `+openAIDailyQuotaPlanSQL+`, COUNT(*) FROM accounts WHERE `+openAIDailyQuotaEligibleSQL+` GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var plan string
		var count int
		if err := rows.Scan(&plan, &count); err != nil {
			return nil, err
		}
		counts[plan] = count
		state.AccountCount += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for plan, count := range counts {
		if count > 0 {
			state.Policy.PlanLimits[plan] = policy.DailyLimit(plan)
			state.Policy.Plan5hLimits[plan] = policy.FiveHourLimit(plan)
		}
		state.Plans = append(state.Plans, service.OpenAIDailyQuotaPlan{PlanType: plan, AccountCount: count})
	}
	sort.Slice(state.Plans, func(i, j int) bool { return state.Plans[i].PlanType < state.Plans[j].PlanType })
	return state, nil
}

func (r *accountRepository) GetOpenAIDailyQuotaState(ctx context.Context) (*service.OpenAIDailyQuotaState, error) {
	policy, err := readOpenAIDailyQuotaPolicy(ctx, r.sql)
	if err != nil {
		return nil, err
	}
	return openAIDailyQuotaState(ctx, r.sql, policy)
}

func (r *accountRepository) SaveOpenAIDailyQuotaPolicy(ctx context.Context, policy service.OpenAIDailyQuotaPolicy) (*service.OpenAIDailyQuotaState, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	db := tx.Client()
	// Serialize policy changes against account creation/import and plan refresh.
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_xact_lock(86420110)`); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW()) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, service.SettingKeyOpenAIDailyQuotaPolicy, string(payload)); err != nil {
		return nil, err
	}
	ids, err := applyOpenAIDailyQuotaPolicySQL(ctx, db, policy, 0)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		if err := enqueueSchedulerOutbox(ctx, db, service.SchedulerOutboxEventAccountBulkChanged, nil, nil, map[string]any{"account_ids": ids}); err != nil {
			return nil, err
		}
	}
	state, err := openAIDailyQuotaState(ctx, db, policy)
	if err != nil {
		return nil, err
	}
	state.AppliedCount = len(ids)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return state, nil
}

// accountID zero applies to the pool; a positive ID reapplies after a token
// refresh changes that account's subscription plan. Usage updates remain atomic.
func applyOpenAIDailyQuotaPolicySQL(ctx context.Context, db sqlExecutor, policy service.OpenAIDailyQuotaPolicy, accountID int64) ([]int64, error) {
	return applyOpenAIDailyQuotaPolicyFilteredSQL(ctx, db, policy, accountID, nil)
}

func applyOpenAIDailyQuotaPolicyToIDsSQL(ctx context.Context, db sqlExecutor, policy service.OpenAIDailyQuotaPolicy, accountIDs []int64) ([]int64, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	return applyOpenAIDailyQuotaPolicyFilteredSQL(ctx, db, policy, 0, accountIDs)
}

func applyOpenAIDailyQuotaPolicyFilteredSQL(ctx context.Context, db sqlExecutor, policy service.OpenAIDailyQuotaPolicy, accountID int64, accountIDs []int64) ([]int64, error) {
	var query string
	var args []any
	scopeParameter := "$1"
	if policy.Enabled {
		scopeParameter = "$7"
	}
	scope := "(" + scopeParameter + "::bigint = 0 OR id = " + scopeParameter + ")"
	var scopeValue any = accountID
	if accountIDs != nil {
		scope = "id = ANY(" + scopeParameter + "::bigint[])"
		scopeValue = pq.Array(accountIDs)
	}
	if policy.Enabled {
		limits, err := json.Marshal(policy.PlanLimits)
		if err != nil {
			return nil, err
		}
		fiveHourLimits, err := json.Marshal(policy.Plan5hLimits)
		if err != nil {
			return nil, err
		}
		last, next := policy.WindowBounds(time.Now())
		query = `UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
			'quota_daily_limit', COALESCE(($1::jsonb ->> (` + openAIDailyQuotaPlanSQL + `))::numeric, ($1::jsonb ->> (` + openAIDailyQuotaFallbackPlanSQL + `))::numeric, $2::numeric),
			'quota_5h_limit', COALESCE(($8::jsonb ->> (` + openAIDailyQuotaPlanSQL + `))::numeric, ($8::jsonb ->> (` + openAIDailyQuotaFallbackPlanSQL + `))::numeric, 0::numeric),
			'quota_daily_reset_mode', 'fixed', 'quota_daily_reset_hour', $3::int,
			'quota_daily_reset_timezone', $4::text, 'quota_daily_reset_at', $6::text,
			'openai_daily_quota_managed', true,
			'quota_daily_used', CASE WHEN COALESCE((extra->>'quota_daily_start')::timestamptz, '1970-01-01'::timestamptz) < $5::timestamptz THEN 0 ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) END,
			'quota_daily_start', CASE WHEN COALESCE((extra->>'quota_daily_start')::timestamptz, '1970-01-01'::timestamptz) < $5::timestamptz THEN $5::text ELSE extra->>'quota_daily_start' END
		), updated_at = NOW() WHERE ` + openAIDailyQuotaEligibleSQL + ` AND ` + scope + ` RETURNING id`
		args = []any{string(limits), 0.0, policy.ResetHour, policy.ResetTimezone, last.UTC().Format(time.RFC3339), next.UTC().Format(time.RFC3339), scopeValue, string(fiveHourLimits)}
	} else {
		query = `UPDATE accounts SET extra = extra - 'quota_daily_limit' - 'quota_daily_reset_mode' - 'quota_daily_reset_hour' - 'quota_daily_reset_at' - 'quota_daily_reset_timezone' - 'quota_5h_limit' - 'openai_daily_quota_managed', updated_at = NOW()
			WHERE ` + openAIDailyQuotaEligibleSQL + ` AND extra->>'openai_daily_quota_managed' = 'true' AND ` + scope + ` RETURNING id`
		args = []any{scopeValue}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
