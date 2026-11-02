package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const automaticModelEvaluationEligibleSQL = `a.deleted_at IS NULL AND a.platform = 'openai' AND a.type = 'oauth'
	AND a.parent_account_id IS NULL AND a.status = 'active'
	AND COALESCE(a.extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
	AND (a.rate_limit_reset_at IS NULL OR a.rate_limit_reset_at <= $1)
	AND (a.overload_until IS NULL OR a.overload_until <= $1)
	AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= $1)
	AND (NOT a.auto_pause_on_expired OR a.expires_at IS NULL OR a.expires_at > $1)`

const claimDueModelEvaluationsSQL = `WITH due AS MATERIALIZED (
	SELECT a.id FROM accounts a
	WHERE ` + automaticModelEvaluationEligibleSQL + `
	AND a.candy_evaluation_next_run_at <= $1
	AND (a.model_evaluation_lease_until IS NULL OR a.model_evaluation_lease_until <= $1)
	ORDER BY a.candy_evaluation_next_run_at, a.id
	LIMIT GREATEST(0, LEAST($3, $4 - (SELECT COUNT(*) FROM accounts running
		WHERE running.model_evaluation_lease_automatic = TRUE AND running.model_evaluation_lease_until > $1)))
	FOR UPDATE OF a SKIP LOCKED
)
UPDATE accounts a SET model_evaluation_lease_token = gen_random_uuid()::text,
	model_evaluation_lease_until = $2, model_evaluation_lease_automatic = TRUE,
	candy_evaluation_next_run_at = $2
FROM due WHERE a.id = due.id
RETURNING a.id, a.model_evaluation_lease_token`

func modelEvaluationLeaseEnd(now time.Time, duration time.Duration) (time.Time, error) {
	if duration <= 0 || duration > service.ModelEvaluationMaxLeaseDuration {
		return time.Time{}, errors.New("model evaluation lease must be between 0 and 2 hours")
	}
	return now.Add(duration), nil
}

func (r *accountRepository) ClaimDueModelEvaluations(ctx context.Context, now time.Time, leaseDuration time.Duration, limit int) ([]service.ModelEvaluationLease, error) {
	if limit <= 0 {
		return []service.ModelEvaluationLease{}, nil
	}
	if limit > 64 {
		limit = 64
	}
	until, err := modelEvaluationLeaseEnd(now, leaseDuration)
	if err != nil {
		return nil, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	db := tx.Client()
	// Locking and counting are separate statements so a claimant waiting for
	// another instance sees that instance's newly committed leases.
	rows, err := db.QueryContext(ctx, `SELECT value::jsonb->>'revision', COALESCE((value::jsonb->>'concurrency')::int, 3)
		FROM settings WHERE key = 'model_evaluation_auto_settings' AND value::jsonb->>'enabled' = 'true' FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		return []service.ModelEvaluationLease{}, err
	}
	var revision string
	var concurrency int
	if err := rows.Scan(&revision, &concurrency); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if revision == "" || concurrency < 1 {
		return []service.ModelEvaluationLease{}, nil
	}
	if concurrency > 10 {
		concurrency = 10
	}
	rows, err = db.QueryContext(ctx, claimDueModelEvaluationsSQL, now, until, limit, concurrency)
	if err != nil {
		return nil, err
	}
	leases := make([]service.ModelEvaluationLease, 0, limit)
	for rows.Next() {
		lease := service.ModelEvaluationLease{Automatic: true, Revision: revision}
		if err := rows.Scan(&lease.AccountID, &lease.Token); err != nil {
			_ = rows.Close()
			return nil, err
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return leases, nil
}

func (r *accountRepository) ClaimModelEvaluation(ctx context.Context, accountID int64, now time.Time, leaseDuration time.Duration) (*service.ModelEvaluationLease, error) {
	until, err := modelEvaluationLeaseEnd(now, leaseDuration)
	if err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, `UPDATE accounts SET model_evaluation_lease_token = gen_random_uuid()::text,
		model_evaluation_lease_until = $3, model_evaluation_lease_automatic = FALSE
		WHERE id = $1 AND deleted_at IS NULL AND platform = 'openai' AND type = 'oauth'
		AND COALESCE(extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
		AND (model_evaluation_lease_until IS NULL OR model_evaluation_lease_until <= $2)
		RETURNING id, model_evaluation_lease_token`, accountID, now, until)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, rows.Err()
	}
	lease := &service.ModelEvaluationLease{}
	if err := rows.Scan(&lease.AccountID, &lease.Token); err != nil {
		return nil, err
	}
	return lease, rows.Err()
}

// The batch projection excludes answer histories and group joins. Probes need
// only account identity, OAuth settings and proxy information, not billing data.
func (r *accountRepository) GetModelEvaluationAccounts(ctx context.Context, ids []int64) ([]*service.Account, error) {
	ids = uniquePositiveInt64s(ids)
	if len(ids) == 0 {
		return []*service.Account{}, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id, name, platform, type, credentials,
		COALESCE(extra, '{}'::jsonb) - 'model_evaluation_results', proxy_id, concurrency,
		status, schedulable, parent_account_id, quota_dimension,
		rate_limit_reset_at, overload_until, temp_unschedulable_until, expires_at, auto_pause_on_expired
		FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	var accounts []*service.Account
	var proxyIDs []int64
	for rows.Next() {
		a := &service.Account{}
		var credentials, extra []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.Platform, &a.Type, &credentials, &extra, &a.ProxyID, &a.Concurrency, &a.Status, &a.Schedulable, &a.ParentAccountID, &a.QuotaDimension,
			&a.RateLimitResetAt, &a.OverloadUntil, &a.TempUnschedulableUntil, &a.ExpiresAt, &a.AutoPauseOnExpired); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(credentials, &a.Credentials); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(extra, &a.Extra); err != nil {
			_ = rows.Close()
			return nil, err
		}
		accounts = append(accounts, a)
		if a.ProxyID != nil {
			proxyIDs = append(proxyIDs, *a.ProxyID)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	proxies, err := r.loadProxies(ctx, proxyIDs)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if account.ProxyID != nil {
			account.Proxy = proxies[*account.ProxyID]
		}
	}
	return accounts, nil
}

const saveModelEvaluationResultSQL = `WITH target AS MATERIALIZED (
	SELECT id, schedulable, ($6::boolean AND status = 'active'
		AND (NOT auto_pause_on_expired OR expires_at IS NULL OR expires_at > NOW())) AS stop_allowed,
		($10::boolean AND status = 'active'
		AND (NOT auto_pause_on_expired OR expires_at IS NULL OR expires_at > NOW())) AS resume_allowed
	FROM accounts WHERE id = $1 AND deleted_at IS NULL AND platform = 'openai' AND type = 'oauth'
	AND model_evaluation_lease_token = $2 AND model_evaluation_lease_automatic = $3
	AND model_evaluation_lease_until > NOW()
	AND COALESCE(extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
	AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(extra->'model_evaluation_results') = 'array' THEN extra->'model_evaluation_results' ELSE '[]'::jsonb END) h WHERE h->>'id' = $5)
	FOR UPDATE
), paused_shadows AS (
	UPDATE accounts shadow SET schedulable = FALSE,
		extra = COALESCE(shadow.extra, '{}'::jsonb) || jsonb_build_object('model_evaluation_pause',
			jsonb_build_object('result_id', $5::text, 'at', $7::text, 'reason', 'candy_wrong', 'parent_account_id', $1::bigint)), updated_at = NOW()
	FROM target t WHERE t.stop_allowed AND shadow.parent_account_id = t.id
	AND shadow.deleted_at IS NULL AND shadow.platform = 'openai' AND shadow.type = 'oauth'
	AND shadow.status = 'active' AND shadow.schedulable = TRUE
	AND COALESCE(shadow.extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
	AND (NOT shadow.auto_pause_on_expired OR shadow.expires_at IS NULL OR shadow.expires_at > NOW())
	RETURNING shadow.id
), resumed_shadows AS (
	UPDATE accounts shadow SET schedulable = TRUE,
		extra = COALESCE(shadow.extra, '{}'::jsonb) - 'model_evaluation_pause', updated_at = NOW()
	FROM target t WHERE t.resume_allowed AND shadow.parent_account_id = t.id
	AND shadow.deleted_at IS NULL AND shadow.platform = 'openai' AND shadow.type = 'oauth'
	AND shadow.status = 'active' AND shadow.schedulable = FALSE
	AND COALESCE(shadow.extra->'synthetic_ui_test', 'false'::jsonb) <> 'true'::jsonb
	AND (NOT shadow.auto_pause_on_expired OR shadow.expires_at IS NULL OR shadow.expires_at > NOW())
	RETURNING shadow.id
), decisions AS (
	SELECT id, (stop_allowed AND schedulable) AS pause_parent,
		(resume_allowed AND NOT schedulable) AS resume_parent,
		((stop_allowed AND schedulable) OR EXISTS (SELECT 1 FROM paused_shadows)) AS stop,
		((resume_allowed AND NOT schedulable) OR EXISTS (SELECT 1 FROM resumed_shadows)) AS resume
	FROM target
), enriched AS (
	SELECT id, stop, resume, pause_parent, resume_parent,
		CASE WHEN stop THEN $4::jsonb || jsonb_build_object(
		'scheduling_stopped', TRUE, 'scheduling_stopped_at', $7::text, 'scheduling_stop_reason', 'candy_wrong')
		WHEN resume THEN $4::jsonb || jsonb_build_object(
		'scheduling_resumed', TRUE, 'scheduling_resumed_at', $7::text, 'scheduling_resume_reason', 'candy_correct')
		ELSE $4::jsonb END AS result FROM decisions
)
UPDATE accounts a SET extra = (CASE WHEN e.resume_parent THEN COALESCE(a.extra, '{}'::jsonb) - 'model_evaluation_pause'
	ELSE COALESCE(a.extra, '{}'::jsonb) END) || jsonb_build_object(
	'model_evaluation_results', (SELECT jsonb_agg(recent.value ORDER BY recent.ordinality) FROM (
		SELECT value, ordinality, SUM(octet_length(value::text) + 1) OVER (ORDER BY ordinality) + 1 AS bytes
		FROM (SELECT value, ordinality FROM jsonb_array_elements(jsonb_build_array(e.result) ||
			CASE WHEN jsonb_typeof(a.extra->'model_evaluation_results') = 'array' THEN a.extra->'model_evaluation_results' ELSE '[]'::jsonb END)
			WITH ORDINALITY h(value, ordinality) ORDER BY ordinality LIMIT 5) latest
	) recent WHERE recent.ordinality = 1 OR recent.bytes <= 1048576)
) || CASE WHEN e.pause_parent THEN jsonb_build_object('model_evaluation_pause', jsonb_build_object('result_id', $5::text, 'at', $7::text, 'reason', 'candy_wrong')) ELSE '{}'::jsonb END,
	schedulable = CASE WHEN e.pause_parent THEN FALSE WHEN e.resume_parent THEN TRUE ELSE a.schedulable END,
	model_evaluation_lease_token = CASE WHEN $9 THEN NULL ELSE a.model_evaluation_lease_token END,
	model_evaluation_lease_until = CASE WHEN $9 THEN NULL ELSE a.model_evaluation_lease_until END,
	model_evaluation_lease_automatic = CASE WHEN $9 THEN FALSE ELSE a.model_evaluation_lease_automatic END,
	candy_evaluation_next_run_at = COALESCE($8::timestamptz, a.candy_evaluation_next_run_at),
	updated_at = NOW()
FROM enriched e WHERE a.id = e.id RETURNING e.stop, e.resume, ARRAY(SELECT id FROM paused_shadows), ARRAY(SELECT id FROM resumed_shadows)`

func (r *accountRepository) SaveModelEvaluationResult(ctx context.Context, lease service.ModelEvaluationLease, result *service.ModelEvaluationResult, nextDue time.Time, release bool) (bool, error) {
	if result == nil || strings.TrimSpace(result.ID) == "" || lease.AccountID <= 0 || lease.Token == "" {
		return false, errors.New("model evaluation result and lease are required")
	}
	result.SchedulingStopped = false
	result.SchedulingStoppedAt, result.SchedulingStopReason = "", ""
	result.SchedulingResumed = false
	result.SchedulingResumedAt, result.SchedulingResumeReason = "", ""
	result.Source = "manual"
	if lease.Automatic {
		result.Source = "automatic"
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	db := tx.Client()
	if lease.Automatic {
		rows, err := db.QueryContext(ctx, `SELECT value::jsonb->>'revision' FROM settings WHERE key = 'model_evaluation_auto_settings' AND value::jsonb->>'enabled' = 'true' FOR SHARE`)
		if err != nil {
			return false, err
		}
		var revision string
		if rows.Next() {
			if err := rows.Scan(&revision); err != nil {
				_ = rows.Close()
				return false, err
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return false, err
		}
		_ = rows.Close()
		if revision == "" || revision != lease.Revision {
			return false, nil
		}
	}
	var next any
	if !nextDue.IsZero() {
		next = nextDue
	}
	stopRequested := result.Kind == "candy" && result.Status == "wrong"
	resumeRequested := result.Kind == "candy" && result.Status == "correct"
	stoppedAt := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := db.QueryContext(ctx, saveModelEvaluationResultSQL, lease.AccountID, lease.Token, lease.Automatic, string(payload), result.ID, stopRequested, stoppedAt, next, release, resumeRequested)
	if err != nil {
		return false, err
	}
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		return false, err
	}
	var stopped, resumed bool
	var pausedShadowIDs, resumedShadowIDs []int64
	if err := rows.Scan(&stopped, &resumed, pq.Array(&pausedShadowIDs), pq.Array(&resumedShadowIDs)); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	var changedIDs []int64
	if stopped || resumed {
		changedIDs = append(changedIDs, lease.AccountID)
		changedIDs = append(changedIDs, pausedShadowIDs...)
		changedIDs = append(changedIDs, resumedShadowIDs...)
		if err := enqueueSchedulerOutbox(ctx, db, service.SchedulerOutboxEventAccountBulkChanged, nil, nil, map[string]any{"account_ids": changedIDs}); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if stopped {
		result.SchedulingStopped = true
		result.SchedulingStoppedAt, result.SchedulingStopReason = stoppedAt, "candy_wrong"
	}
	if resumed {
		result.SchedulingResumed = true
		result.SchedulingResumedAt, result.SchedulingResumeReason = stoppedAt, "candy_correct"
	}
	if stopped || resumed {
		for _, id := range changedIDs {
			r.syncSchedulerAccountSnapshot(ctx, id)
		}
	}
	return true, nil
}

func (r *accountRepository) ReleaseModelEvaluation(ctx context.Context, lease service.ModelEvaluationLease, nextDue time.Time) error {
	var next any
	if !nextDue.IsZero() {
		next = nextDue
	}
	_, err := r.sql.ExecContext(ctx, `UPDATE accounts SET model_evaluation_lease_token = NULL,
		model_evaluation_lease_until = NULL, model_evaluation_lease_automatic = FALSE,
		candy_evaluation_next_run_at = COALESCE($3::timestamptz, candy_evaluation_next_run_at)
		WHERE id = $1 AND model_evaluation_lease_token = $2`, lease.AccountID, lease.Token, next)
	return err
}
