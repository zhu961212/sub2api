package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// Real PostgreSQL coverage: the integration harness supplies its container DSN,
// or callers can set MODEL_EVALUATION_TEST_DSN to opt in without integration tags.
// sqlmock cannot establish MVCC visibility, SKIP LOCKED behavior, JSONB retention,
// or rollback of the account and outbox.
func modelEvaluationPostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("MODEL_EVALUATION_TEST_DSN")
	if dsn == "" {
		t.Skip("MODEL_EVALUATION_TEST_DSN is required for PostgreSQL concurrency tests")
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("model_evaluation_test_%d", time.Now().UnixNano())
	_, err = base.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = base.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = base.Close() })
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TIMESTAMPTZ DEFAULT NOW());
		CREATE TABLE accounts(id BIGINT PRIMARY KEY, name TEXT DEFAULT 'test', platform TEXT DEFAULT 'openai', type TEXT DEFAULT 'oauth',
		credentials JSONB DEFAULT '{}', extra JSONB DEFAULT '{}', proxy_id BIGINT, concurrency INT DEFAULT 3,
		status TEXT DEFAULT 'active', schedulable BOOLEAN DEFAULT TRUE, parent_account_id BIGINT,
		quota_dimension TEXT DEFAULT 'global', auto_pause_on_expired BOOLEAN DEFAULT TRUE, expires_at TIMESTAMPTZ,
		rate_limit_reset_at TIMESTAMPTZ, overload_until TIMESTAMPTZ, temp_unschedulable_until TIMESTAMPTZ,
		deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT NOW());
		CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT);
		CREATE UNIQUE INDEX idx_scheduler_outbox_pending_dedup_key ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
		INSERT INTO accounts(id,platform,extra) VALUES(997,'test','{"model_evaluation_results":[1,2,3,4,5,6,7]}');`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("250_account_model_evaluation_schedule.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT jsonb_array_length(extra->'model_evaluation_results') FROM accounts WHERE id=997`).Scan(&count))
	require.Equal(t, 5, count)
	return db
}

func TestModelEvaluationPostgresConcurrentClaimsHistoryAndAtomicPause(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('model_evaluation_auto_settings','{"enabled":true,"revision":"v1","concurrency":3}');
		INSERT INTO accounts(id) SELECT generate_series(1,10);
		INSERT INTO accounts(id,parent_account_id) VALUES(101,1);
		INSERT INTO accounts(id,schedulable) VALUES(999,FALSE);`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	now := time.Now()
	var batches [2][]service.ModelEvaluationLease
	var errs [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range batches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			batches[i], errs[i] = repo.ClaimDueModelEvaluations(ctx, now, 12*time.Minute, 3)
		}(i)
	}
	close(start)
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	leases := append(batches[0], batches[1]...)
	require.Len(t, leases, 3, "global capacity must apply across both concurrent claimants")
	byID := map[int64]service.ModelEvaluationLease{}
	for _, lease := range leases {
		require.NotContains(t, byID, lease.AccountID)
		byID[lease.AccountID] = lease
	}
	require.Contains(t, byID, int64(1))
	manual, err := repo.ClaimModelEvaluation(ctx, 1, now, time.Hour)
	require.NoError(t, err)
	require.Nil(t, manual, "automatic lease also blocks a manual run")
	wrong := &service.ModelEvaluationResult{ID: "wrong", Kind: "candy", Status: "wrong"}
	saved, err := repo.SaveModelEvaluationResult(ctx, byID[1], wrong, now.Add(12*time.Hour), true)
	require.NoError(t, err)
	require.True(t, saved)
	require.True(t, wrong.SchedulingStopped)
	for _, id := range []int64{1, 101} {
		var schedulable bool
		require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, id).Scan(&schedulable))
		require.False(t, schedulable)
	}
	manual, err = repo.ClaimModelEvaluation(ctx, 4, time.Now(), time.Hour)
	require.NoError(t, err)
	require.NotNil(t, manual)
	for i := 0; i < 7; i++ {
		result := &service.ModelEvaluationResult{ID: fmt.Sprintf("manual-%d", i), Kind: "candy", Status: "wrong"}
		saved, err = repo.SaveModelEvaluationResult(ctx, *manual, result, time.Time{}, false)
		require.NoError(t, err)
		require.True(t, saved)
		require.Equal(t, i == 0, result.SchedulingStopped)
	}
	var raw []byte
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT extra->'model_evaluation_results',schedulable FROM accounts WHERE id=4`).Scan(&raw, &schedulable))
	require.False(t, schedulable, "a completed manual wrong answer pauses scheduling too")
	var history []service.ModelEvaluationResult
	require.NoError(t, json.Unmarshal(raw, &history))
	require.Len(t, history, 5)
	require.Equal(t, "manual-6", history[0].ID)
	require.Equal(t, "manual-2", history[4].ID)
	saved, err = repo.SaveModelEvaluationResult(ctx, *manual, &service.ModelEvaluationResult{ID: "manual-6", Kind: "candy", Status: "wrong"}, time.Time{}, false)
	require.NoError(t, err)
	require.False(t, saved, "repeat completion is idempotent")
	require.NoError(t, repo.ReleaseModelEvaluation(ctx, *manual, time.Time{}))
	newLease, err := repo.ClaimModelEvaluation(ctx, 4, time.Now(), time.Hour)
	require.NoError(t, err)
	require.NotNil(t, newLease)
	require.NotEqual(t, manual.Token, newLease.Token)
	require.NoError(t, repo.ReleaseModelEvaluation(ctx, *manual, time.Time{}))
	var token string
	require.NoError(t, db.QueryRow(`SELECT model_evaluation_lease_token FROM accounts WHERE id=4`).Scan(&token))
	require.Equal(t, newLease.Token, token, "an old worker cannot clear a newer lease")
	largeAnswer := strings.Repeat("x", 200000)
	largeSVG := "<svg><text>" + strings.Repeat("y", 200000) + "</text></svg>"
	for i := 0; i < 3; i++ {
		saved, err = repo.SaveModelEvaluationResult(ctx, *newLease, &service.ModelEvaluationResult{ID: fmt.Sprintf("large-%d", i), Kind: "pelican", Status: "completed", Answer: largeAnswer, SVG: largeSVG}, time.Time{}, false)
		require.NoError(t, err)
		require.True(t, saved)
	}
	var historyBytes int
	require.NoError(t, db.QueryRow(`SELECT extra->'model_evaluation_results',octet_length((extra->'model_evaluation_results')::text) FROM accounts WHERE id=4`).Scan(&raw, &historyBytes))
	require.NoError(t, json.Unmarshal(raw, &history))
	require.Len(t, history, 2)
	require.Equal(t, largeSVG, history[0].SVG, "newest SVG must remain complete")
	require.LessOrEqual(t, historyBytes, 1<<20)
	_, err = db.Exec(`UPDATE settings SET value='{"enabled":false,"revision":"v2","concurrency":3}'`)
	require.NoError(t, err)
	saved, err = repo.SaveModelEvaluationResult(ctx, byID[2], &service.ModelEvaluationResult{ID: "disabled-result", Kind: "candy", Status: "wrong"}, now.Add(12*time.Hour), true)
	require.NoError(t, err)
	require.False(t, saved)
	_, err = db.Exec(`UPDATE settings SET value='{"enabled":true,"revision":"v1","concurrency":3}';
		ALTER TABLE scheduler_outbox ADD CONSTRAINT block_test_outbox CHECK (event_type <> 'account_bulk_changed') NOT VALID`)
	require.NoError(t, err)
	saved, err = repo.SaveModelEvaluationResult(ctx, byID[3], &service.ModelEvaluationResult{ID: "rollback", Kind: "candy", Status: "wrong"}, now.Add(12*time.Hour), true)
	require.Error(t, err)
	require.False(t, saved)
	require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=3`).Scan(&schedulable))
	require.True(t, schedulable, "outbox failure must roll back pause and history")
	var hasResult bool
	require.NoError(t, db.QueryRow(`SELECT extra ? 'model_evaluation_results' FROM accounts WHERE id=3`).Scan(&hasResult))
	require.False(t, hasResult)
}

func TestModelEvaluationPostgresSkipsCooldownsUntilTheirWindowsExpire(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	now := time.Now().UTC()
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('model_evaluation_auto_settings','{"enabled":true,"revision":"v1","concurrency":10}');
		INSERT INTO accounts(id) SELECT generate_series(1,6);`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET candy_evaluation_next_run_at=$1,
		rate_limit_reset_at=CASE WHEN id=1 THEN $2 WHEN id=5 THEN $1 ELSE rate_limit_reset_at END,
		overload_until=CASE WHEN id=2 THEN $2 WHEN id=5 THEN $1 ELSE overload_until END,
		temp_unschedulable_until=CASE WHEN id=3 THEN $2 WHEN id=5 THEN $1 ELSE temp_unschedulable_until END,
		expires_at=CASE WHEN id IN (4,6) THEN $1 ELSE expires_at END,
		auto_pause_on_expired=CASE WHEN id=6 THEN FALSE ELSE auto_pause_on_expired END`, now.Add(-time.Hour), now.Add(30*time.Minute))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	leases, err := repo.ClaimDueModelEvaluations(context.Background(), now, 12*time.Minute, 10)
	require.NoError(t, err)
	var firstIDs []int64
	for _, lease := range leases {
		firstIDs = append(firstIDs, lease.AccountID)
		require.NoError(t, repo.ReleaseModelEvaluation(context.Background(), lease, now.Add(3*time.Hour)))
	}
	require.ElementsMatch(t, []int64{5, 6}, firstIDs)
	leases, err = repo.ClaimDueModelEvaluations(context.Background(), now.Add(time.Hour), 12*time.Minute, 10)
	require.NoError(t, err)
	var resumedIDs []int64
	for _, lease := range leases {
		resumedIDs = append(resumedIDs, lease.AccountID)
	}
	require.ElementsMatch(t, []int64{1, 2, 3}, resumedIDs, "cooldown expiry restores eligibility without modifying scheduling")
}

func TestModelEvaluationPostgresTestsStoppedAccountsAndRestoresCorrectAnswers(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	for _, name := range []string{"251_account_model_evaluation_due_index_notx.sql", "252_account_model_evaluation_stopped_due_index_notx.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		for _, statement := range splitSQLStatements(string(migration)) {
			_, err = db.Exec(statement)
			require.NoError(t, err)
		}
	}
	var predicate string
	require.NoError(t, db.QueryRow(`SELECT pg_get_expr(indpred, indrelid) FROM pg_index
		WHERE indexrelid = 'idx_accounts_candy_evaluation_all_due'::regclass`).Scan(&predicate))
	require.NotContains(t, predicate, "schedulable", "the due index must cover stopped accounts too")

	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('model_evaluation_auto_settings','{"enabled":true,"revision":"v1","concurrency":3}');
		INSERT INTO accounts(id,schedulable) VALUES(1,FALSE),(2,FALSE);`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	now := time.Now()
	leases, err := repo.ClaimDueModelEvaluations(context.Background(), now, 12*time.Minute, 3)
	require.NoError(t, err)
	require.Len(t, leases, 2, "scheduling stopped accounts still receive automatic diagnostics")
	for _, lease := range leases {
		status := "correct"
		if lease.AccountID == 2 {
			status = "wrong"
		}
		result := &service.ModelEvaluationResult{ID: fmt.Sprintf("stopped-%d", lease.AccountID), Kind: "candy", Status: status}
		saved, err := repo.SaveModelEvaluationResult(context.Background(), lease, result, now.Add(12*time.Hour), true)
		require.NoError(t, err)
		require.True(t, saved)
		require.False(t, result.SchedulingStopped, "an already stopped account is not stopped again")
		var schedulable bool
		var recordedStatus string
		require.NoError(t, db.QueryRow(`SELECT schedulable, extra->'model_evaluation_results'->0->>'status' FROM accounts WHERE id=$1`, lease.AccountID).Scan(&schedulable, &recordedStatus))
		require.Equal(t, status == "correct", schedulable, "correct answers restore manual stops too")
		require.Equal(t, status == "correct", result.SchedulingResumed)
		require.Equal(t, status, recordedStatus)
	}
	leases, err = repo.ClaimDueModelEvaluations(context.Background(), now.Add(time.Hour), 12*time.Minute, 3)
	require.NoError(t, err)
	require.Empty(t, leases, "stopped accounts respect the configured interval")
	leases, err = repo.ClaimDueModelEvaluations(context.Background(), now.Add(13*time.Hour), 12*time.Minute, 3)
	require.NoError(t, err)
	require.Len(t, leases, 2, "stopped accounts remain eligible for later recovery checks")
}

func TestModelEvaluationPostgresWrongCorrectClosesSchedulingLoop(t *testing.T) {
	db := modelEvaluationPostgresDB(t)
	ctx := context.Background()
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES('model_evaluation_auto_settings','{"enabled":true,"revision":"v1","concurrency":3}');
		INSERT INTO accounts(id,extra) VALUES(1,'{"quota_daily_used":12,"quota_5h_used":3}');
		INSERT INTO accounts(id,parent_account_id,schedulable) VALUES(2,1,TRUE),(3,1,FALSE);`)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	sequence := 0
	evaluate := func(status string) *service.ModelEvaluationResult {
		t.Helper()
		sequence++
		token := fmt.Sprintf("closed-loop-%d", sequence)
		_, err := db.Exec(`UPDATE accounts SET model_evaluation_lease_token=$1, model_evaluation_lease_automatic=TRUE,
			model_evaluation_lease_until=NOW()+INTERVAL '1 hour' WHERE id=1`, token)
		require.NoError(t, err)
		result := &service.ModelEvaluationResult{ID: token, Kind: "candy", Status: status}
		saved, err := repo.SaveModelEvaluationResult(ctx, service.ModelEvaluationLease{
			AccountID: 1, Token: token, Automatic: true, Revision: "v1",
		}, result, time.Now().Add(12*time.Hour), true)
		require.NoError(t, err)
		require.True(t, saved)
		return result
	}
	assertScheduling := func(parent, shadow, manuallyStoppedShadow bool) {
		t.Helper()
		for id, want := range map[int64]bool{1: parent, 2: shadow, 3: manuallyStoppedShadow} {
			var actual bool
			require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, id).Scan(&actual))
			require.Equal(t, want, actual, "account %d", id)
		}
	}
	require.True(t, evaluate("wrong").SchedulingStopped)
	assertScheduling(false, false, false)
	require.False(t, evaluate("failed").SchedulingResumed)
	assertScheduling(false, false, false)
	recovered := evaluate("correct")
	require.True(t, recovered.SchedulingResumed)
	require.Equal(t, "candy_correct", recovered.SchedulingResumeReason)
	assertScheduling(true, true, true)
	var daily, fiveHour float64
	require.NoError(t, db.QueryRow(`SELECT (extra->>'quota_daily_used')::float8,(extra->>'quota_5h_used')::float8 FROM accounts WHERE id=1`).Scan(&daily, &fiveHour))
	require.Equal(t, 12.0, daily)
	require.Equal(t, 3.0, fiveHour)

	require.True(t, evaluate("wrong").SchedulingStopped)
	require.NoError(t, repo.SetSchedulable(ctx, 1, false))
	require.NoError(t, repo.SetSchedulable(ctx, 2, false))
	require.True(t, evaluate("correct").SchedulingResumed, "a correct answer also restores explicit manual stops")
	assertScheduling(true, true, true)
}
func TestModelEvaluationPostgresStoppedParentStillProtectsActiveShadows(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status              string
		automatic, wantStop, wantResume bool
	}{
		{name: "automatic_wrong", kind: "candy", status: "wrong", automatic: true, wantStop: true},
		{name: "automatic_correct", kind: "candy", status: "correct", automatic: true, wantResume: true},
		{name: "automatic_failed", kind: "candy", status: "failed", automatic: true},
		{name: "manual_wrong", kind: "candy", status: "wrong", wantStop: true},
		{name: "manual_correct", kind: "candy", status: "correct", wantResume: true},
		{name: "manual_pelican", kind: "pelican", status: "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := modelEvaluationPostgresDB(t)
			ctx := context.Background()
			_, err := db.Exec(`CREATE TABLE proxies(id BIGINT PRIMARY KEY,name TEXT,deleted_at TIMESTAMPTZ);
				INSERT INTO settings(key,value) VALUES('model_evaluation_auto_settings','{"enabled":true,"revision":"v1","concurrency":3}');
				INSERT INTO accounts(id,schedulable) VALUES(1,FALSE);
				INSERT INTO accounts(id,parent_account_id,schedulable) VALUES(2,1,TRUE);`)
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := newAccountRepositoryWithSQL(client, db, nil)
			var lease *service.ModelEvaluationLease
			if tc.automatic {
				leases, err := repo.ClaimDueModelEvaluations(ctx, time.Now(), 12*time.Minute, 3)
				require.NoError(t, err)
				require.Len(t, leases, 1, "the stopped parent is eligible and its shadow is not a separate automatic test")
				lease = &leases[0]
			} else {
				lease, err = repo.ClaimModelEvaluation(ctx, 1, time.Now(), 12*time.Minute)
				require.NoError(t, err)
				require.NotNil(t, lease)
			}
			result := &service.ModelEvaluationResult{ID: "test-result", Kind: tc.kind, Status: tc.status, SchedulingStopped: true}
			saved, err := repo.SaveModelEvaluationResult(ctx, *lease, result, time.Now().Add(12*time.Hour), true)
			require.NoError(t, err)
			require.True(t, saved)
			require.Equal(t, tc.wantStop, result.SchedulingStopped)
			require.Equal(t, tc.wantResume, result.SchedulingResumed)
			var parentSchedulable, shadowSchedulable, recordedStop bool
			require.NoError(t, db.QueryRow(`SELECT schedulable,(extra->'model_evaluation_results'->0->>'scheduling_stopped')::boolean FROM accounts WHERE id=1`).Scan(&parentSchedulable, &recordedStop))
			require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=2`).Scan(&shadowSchedulable))
			require.Equal(t, tc.wantResume, parentSchedulable, "only a correct candy answer restores the parent")
			require.Equal(t, !tc.wantStop, shadowSchedulable, "only a fresh automatic wrong candy result may pause the active shadow")
			require.Equal(t, tc.wantStop, recordedStop, "the persisted history and returned verdict must agree")
			var events int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM scheduler_outbox`).Scan(&events))
			if tc.wantStop || tc.wantResume {
				require.Equal(t, 1, events)
			} else {
				require.Zero(t, events)
			}
			if tc.wantStop {
				_, err = db.Exec(`UPDATE accounts SET schedulable=TRUE WHERE id=2`)
				require.NoError(t, err)
				saved, err = repo.SaveModelEvaluationResult(ctx, *lease, result, time.Now().Add(12*time.Hour), true)
				require.NoError(t, err)
				require.False(t, saved, "replaying an old result after manual recovery must not pause the shadow again")
			}
			_, err = repo.GetModelEvaluationHistory(ctx, []int64{1})
			require.NoError(t, err)
			require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=2`).Scan(&shadowSchedulable))
			require.True(t, shadowSchedulable, "reading historical wrong answers must never change current scheduling")
		})
	}
}
