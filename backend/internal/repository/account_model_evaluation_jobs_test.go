package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func modelEvaluationJobRepo(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	return newAccountRepositoryWithSQL(client, db, nil), mock
}

func TestModelEvaluationClaimSerializesGlobalCapacityBeforeBoundedRead(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*concurrency.*FROM settings.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"revision", "concurrency"}).AddRow("revision-1", 3))
	mock.ExpectQuery(regexp.QuoteMeta(claimDueModelEvaluationsSQL)).WithArgs(now, now.Add(12*time.Minute), 64, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "token"}).AddRow(1, "lease-one").AddRow(2, "lease-two"))
	mock.ExpectCommit()
	leases, err := repo.ClaimDueModelEvaluations(context.Background(), now, 12*time.Minute, 1000)
	require.NoError(t, err)
	require.Len(t, leases, 2)
	require.Equal(t, "revision-1", leases[0].Revision)
	require.True(t, leases[0].Automatic)
	require.Contains(t, claimDueModelEvaluationsSQL, "SKIP LOCKED")
	require.Contains(t, claimDueModelEvaluationsSQL, "SELECT COUNT(*)")
	require.Contains(t, claimDueModelEvaluationsSQL, "model_evaluation_lease_until > $1")
	require.NotContains(t, claimDueModelEvaluationsSQL, "model_evaluation_results")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationClaimDisabledDoesNotReadAccounts(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"revision", "concurrency"}))
	mock.ExpectRollback()
	leases, err := repo.ClaimDueModelEvaluations(context.Background(), time.Now(), time.Minute, 3)
	require.NoError(t, err)
	require.Empty(t, leases)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationManualClaimRespectsExistingLease(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	now := time.Now()
	mock.ExpectQuery(`(?s)UPDATE accounts.*model_evaluation_lease_until <= \$2.*RETURNING`).WithArgs(int64(7), now, now.Add(time.Hour)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "token"}))
	lease, err := repo.ClaimModelEvaluation(context.Background(), 7, now, time.Hour)
	require.NoError(t, err)
	require.Nil(t, lease)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationSaveDoesNotChangeSchedulingWithoutAnOwnedTransition(t *testing.T) {
	for _, tc := range []struct {
		source       bool
		kind, status string
	}{{true, "candy", "correct"}, {true, "candy", "failed"}, {false, "candy", "wrong"}, {false, "pelican", "completed"}} {
		t.Run(tc.kind+tc.status+map[bool]string{true: "auto", false: "manual"}[tc.source], func(t *testing.T) {
			repo, mock := modelEvaluationJobRepo(t)
			lease := service.ModelEvaluationLease{AccountID: 7, Token: "owner", Automatic: tc.source, Revision: "v1"}
			result := &service.ModelEvaluationResult{ID: "result", Kind: tc.kind, Status: tc.status, SchedulingStopped: true}
			mock.ExpectBegin()
			if tc.source {
				mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR SHARE`).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow("v1"))
			}
			stopRequested := tc.kind == "candy" && tc.status == "wrong"
			resumeRequested := tc.kind == "candy" && tc.status == "correct"
			mock.ExpectQuery(regexp.QuoteMeta(saveModelEvaluationResultSQL)).WithArgs(int64(7), "owner", tc.source, sqlmock.AnyArg(), "result", stopRequested, sqlmock.AnyArg(), nil, true, resumeRequested).
				WillReturnRows(sqlmock.NewRows([]string{"stop", "resume", "paused_shadow_ids", "resumed_shadow_ids"}).AddRow(false, false, "{}", "{}"))
			mock.ExpectCommit()
			saved, err := repo.SaveModelEvaluationResult(context.Background(), lease, result, time.Time{}, true)
			require.NoError(t, err)
			require.True(t, saved)
			require.False(t, result.SchedulingStopped)
			require.False(t, result.SchedulingResumed)
			require.Empty(t, result.SchedulingStoppedAt)
			require.NoError(t, mock.ExpectationsWereMet(), "normal results must not touch shadow accounts or scheduler outbox")
		})
	}
}

func TestModelEvaluationWrongStopsParentAndShadowsAtomically(t *testing.T) {
	for _, failOutbox := range []bool{false, true} {
		t.Run(map[bool]string{true: "rollback", false: "commit"}[failOutbox], func(t *testing.T) {
			repo, mock := modelEvaluationJobRepo(t)
			lease := service.ModelEvaluationLease{AccountID: 7, Token: "owner", Automatic: true, Revision: "v1"}
			result := &service.ModelEvaluationResult{ID: "wrong-result", Kind: "candy", Status: "wrong"}
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR SHARE`).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow("v1"))
			mock.ExpectQuery(regexp.QuoteMeta(saveModelEvaluationResultSQL)).WithArgs(int64(7), "owner", true, sqlmock.AnyArg(), "wrong-result", true, sqlmock.AnyArg(), nil, true, false).
				WillReturnRows(sqlmock.NewRows([]string{"stop", "resume", "paused_shadow_ids", "resumed_shadow_ids"}).AddRow(true, false, "{8}", "{}"))
			outbox := mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{7, 8}})
			if failOutbox {
				outbox.WillReturnError(errors.New("outbox unavailable"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			saved, err := repo.SaveModelEvaluationResult(context.Background(), lease, result, time.Time{}, true)
			if failOutbox {
				require.Error(t, err)
				require.False(t, saved)
				require.False(t, result.SchedulingStopped)
			} else {
				require.NoError(t, err)
				require.True(t, saved)
				require.True(t, result.SchedulingStopped)
				require.Equal(t, "candy_wrong", result.SchedulingStopReason)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestModelEvaluationWrongWithoutSchedulingChangesDoesNotPublishOutbox(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	lease := service.ModelEvaluationLease{AccountID: 7, Token: "owner", Automatic: true, Revision: "v1"}
	result := &service.ModelEvaluationResult{ID: "already-stopped", Kind: "candy", Status: "wrong", SchedulingStopped: true}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR SHARE`).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow("v1"))
	mock.ExpectQuery(regexp.QuoteMeta(saveModelEvaluationResultSQL)).
		WithArgs(int64(7), "owner", true, sqlmock.AnyArg(), "already-stopped", true, sqlmock.AnyArg(), nil, true, false).
		WillReturnRows(sqlmock.NewRows([]string{"stop", "resume", "paused_shadow_ids", "resumed_shadow_ids"}).AddRow(false, false, "{}", "{}"))
	mock.ExpectCommit()
	saved, err := repo.SaveModelEvaluationResult(context.Background(), lease, result, time.Time{}, true)
	require.NoError(t, err)
	require.True(t, saved)
	require.False(t, result.SchedulingStopped, "an already stopped account and its stopped shadows do not create a new scheduling change")
	require.NoError(t, mock.ExpectationsWereMet(), "recording a verdict without a scheduling change must not publish an outbox event")
}

func TestModelEvaluationCorrectRestoresParentAndOwnedShadowsAtomically(t *testing.T) {
	for _, failOutbox := range []bool{false, true} {
		t.Run(map[bool]string{true: "rollback", false: "commit"}[failOutbox], func(t *testing.T) {
			repo, mock := modelEvaluationJobRepo(t)
			lease := service.ModelEvaluationLease{AccountID: 7, Token: "owner", Automatic: true, Revision: "v1"}
			result := &service.ModelEvaluationResult{ID: "correct-result", Kind: "candy", Status: "correct"}
			mock.ExpectBegin()
			mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR SHARE`).WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow("v1"))
			mock.ExpectQuery(regexp.QuoteMeta(saveModelEvaluationResultSQL)).
				WithArgs(int64(7), "owner", true, sqlmock.AnyArg(), "correct-result", false, sqlmock.AnyArg(), nil, true, true).
				WillReturnRows(sqlmock.NewRows([]string{"stop", "resume", "paused_shadow_ids", "resumed_shadow_ids"}).AddRow(false, true, "{}", "{8}"))
			outbox := mock.ExpectExec("INSERT INTO scheduler_outbox").
				WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{7, 8}})
			if failOutbox {
				outbox.WillReturnError(errors.New("outbox unavailable"))
				mock.ExpectRollback()
			} else {
				outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			saved, err := repo.SaveModelEvaluationResult(context.Background(), lease, result, time.Time{}, true)
			if failOutbox {
				require.Error(t, err)
				require.False(t, saved)
				require.False(t, result.SchedulingResumed)
			} else {
				require.NoError(t, err)
				require.True(t, saved)
				require.True(t, result.SchedulingResumed)
				require.Equal(t, "candy_correct", result.SchedulingResumeReason)
			}
			require.False(t, result.SchedulingStopped)
			require.Contains(t, saveModelEvaluationResultSQL, "resume_allowed AND NOT schedulable")
			require.NotContains(t, saveModelEvaluationResultSQL, "AND evaluation_paused")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestModelEvaluationSaveRejectsDisabledOrReconfiguredAutomation(t *testing.T) {
	for _, revision := range []string{"", "new-settings"} {
		repo, mock := modelEvaluationJobRepo(t)
		mock.ExpectBegin()
		rows := sqlmock.NewRows([]string{"revision"})
		if revision != "" {
			rows.AddRow(revision)
		}
		mock.ExpectQuery(`(?s)SELECT .*FROM settings.*FOR SHARE`).WillReturnRows(rows)
		mock.ExpectRollback()
		saved, err := repo.SaveModelEvaluationResult(context.Background(), service.ModelEvaluationLease{AccountID: 7, Token: "old-owner", Automatic: true, Revision: "old-settings"}, &service.ModelEvaluationResult{ID: "result", Kind: "candy", Status: "wrong"}, time.Time{}, true)
		require.NoError(t, err)
		require.False(t, saved)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestModelEvaluationSaveFencesLostLeaseAndDuplicateResults(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(saveModelEvaluationResultSQL)).WillReturnRows(sqlmock.NewRows([]string{"stop"}))
	mock.ExpectRollback()
	saved, err := repo.SaveModelEvaluationResult(context.Background(), service.ModelEvaluationLease{AccountID: 7, Token: "stale"}, &service.ModelEvaluationResult{ID: "duplicate", Kind: "candy", Status: "wrong"}, time.Time{}, false)
	require.NoError(t, err)
	require.False(t, saved)
	require.Contains(t, saveModelEvaluationResultSQL, "model_evaluation_lease_token = $2")
	require.Contains(t, saveModelEvaluationResultSQL, "model_evaluation_lease_until > NOW()")
	require.Contains(t, saveModelEvaluationResultSQL, "NOT EXISTS")
	require.Contains(t, saveModelEvaluationResultSQL, "LIMIT 5")
	require.Contains(t, saveModelEvaluationResultSQL, "SUM(octet_length(value::text) + 1)")
	require.Contains(t, saveModelEvaluationResultSQL, "recent.ordinality = 1 OR recent.bytes <= 1048576")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationReleasePreservesDueUnlessExplicitlyRescheduled(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	mock.ExpectExec(`(?s)UPDATE accounts.*COALESCE\(\$3::timestamptz, candy_evaluation_next_run_at\).*WHERE id = \$1 AND model_evaluation_lease_token = \$2`).WithArgs(int64(7), "owner", nil).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.ReleaseModelEvaluation(context.Background(), service.ModelEvaluationLease{AccountID: 7, Token: "owner"}, time.Time{}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationBatchProjectionExcludesHistoryAndGroups(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	resetAt := time.Now().Add(time.Hour).Truncate(time.Second)
	mock.ExpectQuery(`(?s)SELECT .* - 'model_evaluation_results'.*FROM accounts WHERE id = ANY`).WithArgs("{7,8}").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "type", "credentials", "extra", "proxy_id", "concurrency", "status", "schedulable", "parent_account_id", "quota_dimension", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until", "expires_at", "auto_pause_on_expired"}).AddRow(7, "account", "openai", "oauth", []byte(`{"access_token":"test"}`), []byte(`{}`), nil, 3, "active", true, nil, "global", resetAt, nil, nil, nil, true))
	accounts, err := repo.GetModelEvaluationAccounts(context.Background(), []int64{7, 8, 7})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "test", accounts[0].GetCredential("access_token"))
	require.Equal(t, resetAt, *accounts[0].RateLimitResetAt)
	require.True(t, accounts[0].AutoPauseOnExpired)
	require.NoError(t, mock.ExpectationsWereMet(), "no per-account queries or group joins")
}

func TestModelEvaluationExplicitResumeClearsPauseWithoutResettingQuota(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	mock.ExpectExec(`(?s)UPDATE accounts SET schedulable = \$1.* - 'model_evaluation_pause'.*WHERE id = \$2`).
		WithArgs(true, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.SetSchedulable(context.Background(), 7, true))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestModelEvaluationManualToggleClearsStalePauseMarker(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	mock.ExpectExec(`(?s)UPDATE accounts SET schedulable = \$1.* - 'model_evaluation_pause'.*WHERE id = \$2`).
		WithArgs(false, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountChanged, int64(7), nil, nil, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.SetSchedulable(context.Background(), 7, false))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestModelEvaluationBulkResumeClearsPauseAtomically(t *testing.T) {
	repo, mock := modelEvaluationJobRepo(t)
	resume := true
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts SET schedulable = \$1, extra = .* - 'model_evaluation_pause'.*WHERE id = ANY`).
		WithArgs(true, "{7,8}").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{7, 8}}).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	changed, err := repo.BulkUpdate(context.Background(), []int64{7, 8}, service.AccountBulkUpdate{Schedulable: &resume})
	require.NoError(t, err)
	require.EqualValues(t, 2, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}
