//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type modelEvaluationAutoRepo struct {
	mockAccountRepoForGemini
	mu                   sync.Mutex
	settings             *upstreamBillingProbeSettingRepo
	leases               map[int64]ModelEvaluationLease
	next                 map[int64]time.Time
	saved                []ModelEvaluationResult
	maxBatch             int
	batchReads           int
	sequence             int
	releaseDue           []time.Time
	manualLeaseDurations []time.Duration
}

func (r *modelEvaluationAutoRepo) ClaimDueModelEvaluations(ctx context.Context, now time.Time, duration time.Duration, limit int) ([]ModelEvaluationLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	raw, _ := r.settings.GetValue(ctx, SettingKeyModelEvaluationAutoSettings)
	var settings ModelEvaluationAutoSettings
	_ = json.Unmarshal([]byte(raw), &settings)
	if !settings.Enabled {
		return nil, nil
	}
	var result []ModelEvaluationLease
	for id := range r.accountsByID {
		if len(result) == limit {
			break
		}
		if _, leased := r.leases[id]; leased || r.next[id].After(now) {
			continue
		}
		r.sequence++
		lease := ModelEvaluationLease{AccountID: id, Token: fmt.Sprint(r.sequence), Automatic: true, Revision: settings.Revision}
		r.leases[id] = lease
		r.next[id] = now.Add(duration)
		result = append(result, lease)
	}
	if len(result) > r.maxBatch {
		r.maxBatch = len(result)
	}
	return result, nil
}

func (r *modelEvaluationAutoRepo) ClaimModelEvaluation(_ context.Context, id int64, _ time.Time, duration time.Duration) (*ModelEvaluationLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.leases[id]; exists {
		return nil, nil
	}
	r.sequence++
	lease := ModelEvaluationLease{AccountID: id, Token: fmt.Sprint(r.sequence)}
	r.leases[id] = lease
	r.manualLeaseDurations = append(r.manualLeaseDurations, duration)
	return &lease, nil
}

func (r *modelEvaluationAutoRepo) GetModelEvaluationAccounts(ctx context.Context, ids []int64) ([]*Account, error) {
	r.mu.Lock()
	r.batchReads++
	r.mu.Unlock()
	return r.mockAccountRepoForGemini.GetByIDs(ctx, ids)
}

func (r *modelEvaluationAutoRepo) SaveModelEvaluationResult(ctx context.Context, lease ModelEvaluationLease, result *ModelEvaluationResult, next time.Time, release bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	current, ok := r.leases[lease.AccountID]
	if !ok || current.Token != lease.Token {
		return false, nil
	}
	if lease.Automatic {
		raw, _ := r.settings.GetValue(ctx, SettingKeyModelEvaluationAutoSettings)
		var settings ModelEvaluationAutoSettings
		_ = json.Unmarshal([]byte(raw), &settings)
		if !settings.Enabled || settings.Revision != lease.Revision {
			return false, nil
		}
	}
	account := r.accountsByID[lease.AccountID]
	if account != nil && result.Kind == "candy" {
		if result.Status == "wrong" {
			result.SchedulingStopped = account.Schedulable
			account.Schedulable = false
		} else if result.Status == "correct" {
			result.SchedulingResumed = !account.Schedulable
			account.Schedulable = true
		}
	}
	r.saved = append(r.saved, *result)
	if !next.IsZero() {
		r.next[lease.AccountID] = next
	}
	if release {
		delete(r.leases, lease.AccountID)
	}
	return true, nil
}

func (r *modelEvaluationAutoRepo) ReleaseModelEvaluation(_ context.Context, lease ModelEvaluationLease, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.releaseDue = append(r.releaseDue, next)
	if current, ok := r.leases[lease.AccountID]; ok && current.Token == lease.Token {
		delete(r.leases, lease.AccountID)
		if !next.IsZero() {
			r.next[lease.AccountID] = next
		}
	}
	return nil
}

type modelEvaluationAutoUpstream struct {
	calls  atomic.Int64
	active atomic.Int64
	peak   atomic.Int64
	block  bool
	status int
}

func (u *modelEvaluationAutoUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		peak := u.peak.Load()
		if active <= peak || u.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	if u.block {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(10 * time.Millisecond):
	}
	status := u.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(evaluationCompletedStream("21")))}, nil
}
func (u *modelEvaluationAutoUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func newModelEvaluationAutoFixture(t *testing.T, count int, upstream *modelEvaluationAutoUpstream) (*ModelEvaluationAutoService, *modelEvaluationAutoRepo, *AccountTestService) {
	t.Helper()
	settings := DefaultModelEvaluationAutoSettings()
	settings.Enabled = true
	settings.Revision = "revision-1"
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	settingRepo := &upstreamBillingProbeSettingRepo{values: map[string]string{SettingKeyModelEvaluationAutoSettings: string(raw)}}
	repo := &modelEvaluationAutoRepo{settings: settingRepo, leases: make(map[int64]ModelEvaluationLease), next: make(map[int64]time.Time)}
	repo.accountsByID = make(map[int64]*Account)
	for id := int64(1); id <= int64(count); id++ {
		repo.accountsByID[id] = &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "test-token"}}
	}
	settingsService := &SettingService{settingRepo: settingRepo}
	tester := &AccountTestService{accountRepo: repo, httpUpstream: upstream, settingService: settingsService}
	auto := NewModelEvaluationAutoService(repo, tester, settingsService)
	auto.pollInterval = time.Hour // Completion notifications must replenish capacity without waiting for this ticker.
	tester.modelEvaluationAuto = auto
	t.Cleanup(auto.Stop)
	return auto, repo, tester
}

func TestModelEvaluationAutoBoundedWorkersImmediatelyRefill(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{}
	auto, repo, _ := newModelEvaluationAutoFixture(t, 12, upstream)
	auto.Start()
	require.Eventually(t, func() bool { repo.mu.Lock(); defer repo.mu.Unlock(); return len(repo.saved) == 12 }, 3*time.Second, time.Millisecond)
	auto.Stop()
	require.LessOrEqual(t, upstream.peak.Load(), int64(3))
	require.Greater(t, upstream.peak.Load(), int64(1))
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.LessOrEqual(t, repo.maxBatch, 3)
	require.Greater(t, repo.batchReads, 1)
	require.Empty(t, repo.leases)
	for _, result := range repo.saved {
		require.Equal(t, "automatic", result.Source)
		require.Equal(t, "candy", result.Kind)
		require.Equal(t, "correct", result.Status)
		require.Equal(t, 1, result.Runs)
		require.False(t, result.SchedulingStopped)
	}
}

func TestModelEvaluationAutoDisableCancelsAndSharesManualLease(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{block: true}
	auto, repo, tester := newModelEvaluationAutoFixture(t, 3, upstream)
	auto.Start()
	require.Eventually(t, func() bool { return upstream.active.Load() == 3 }, 2*time.Second, time.Millisecond)
	ctx, recorder := newTestContext()
	require.Error(t, tester.EvaluateOpenAIAccount(ctx, 1, ModelEvaluationRequest{}))
	require.Empty(t, recorder.Body.String())
	require.Equal(t, int64(3), upstream.calls.Load())
	settings := DefaultModelEvaluationAutoSettings()
	updated, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), settings)
	require.NoError(t, err)
	require.False(t, updated.Enabled)
	require.NotEqual(t, "revision-1", updated.Revision)
	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return upstream.active.Load() == 0 && len(repo.leases) == 0
	}, 2*time.Second, time.Millisecond)
	auto.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Empty(t, repo.saved, "disabled in-flight requests must not be graded or committed")
	for _, due := range repo.releaseDue {
		require.True(t, due.IsZero(), "cancellation must preserve the claimed retry window")
	}
	for _, due := range repo.next {
		require.True(t, due.After(time.Now().Add(modelEvaluationTimeout)), "cancellation must not shorten the lease window to one minute")
	}
}

func TestModelEvaluationManualRunsDifferentAccountsConcurrently(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{block: true}
	_, repo, tester := newModelEvaluationAutoFixture(t, 2, upstream)
	repo.accountsByID[1].Schedulable = false
	first, _ := newTestContext()
	second, _ := newTestContext()
	duplicate, recorder := newTestContext()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first.Request = first.Request.WithContext(ctx)
	second.Request = second.Request.WithContext(ctx)
	done := make(chan error, 2)
	go func() { done <- tester.EvaluateOpenAIAccount(first, 1, ModelEvaluationRequest{}) }()
	go func() { done <- tester.EvaluateOpenAIAccount(second, 2, ModelEvaluationRequest{}) }()
	require.Eventually(t, func() bool { return upstream.active.Load() == 2 }, 2*time.Second, time.Millisecond)
	require.Error(t, tester.EvaluateOpenAIAccount(duplicate, 1, ModelEvaluationRequest{}), "same-account requests are still deduplicated")
	require.Empty(t, recorder.Body.String())
	require.Equal(t, int64(2), upstream.calls.Load())
	cancel()
	for range 2 {
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(2 * time.Second):
			t.Fatal("manual evaluation did not stop after cancellation")
		}
	}
	require.Empty(t, repo.leases)
	require.False(t, repo.accountsByID[1].Schedulable)
}

func TestModelEvaluationAutoShutdownReleasesAllClaims(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{block: true}
	auto, repo, _ := newModelEvaluationAutoFixture(t, 10, upstream)
	auto.Start()
	require.Eventually(t, func() bool { return upstream.active.Load() > 0 }, 2*time.Second, time.Millisecond)
	stopped := make(chan struct{})
	go func() { auto.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("auto evaluator did not stop")
	}
	require.Zero(t, upstream.active.Load())
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Empty(t, repo.leases)
	require.Empty(t, repo.saved)
}

func TestModelEvaluationAutoRequestFailureOnlyRecordsFailed(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{status: http.StatusTooManyRequests}
	auto, repo, _ := newModelEvaluationAutoFixture(t, 1, upstream)
	auto.Start()
	require.Eventually(t, func() bool { repo.mu.Lock(); defer repo.mu.Unlock(); return len(repo.saved) == 1 }, 2*time.Second, time.Millisecond)
	auto.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, "failed", repo.saved[0].Status)
	require.Equal(t, "automatic", repo.saved[0].Source)
	require.False(t, repo.saved[0].SchedulingStopped)
	require.True(t, repo.accountsByID[1].Schedulable)
}

func TestModelEvaluationAutoTestsStoppedAccountsAndRestoresCorrectAnswers(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{}
	auto, repo, _ := newModelEvaluationAutoFixture(t, 1, upstream)
	repo.accountsByID[1].Schedulable = false
	auto.Start()
	require.Eventually(t, func() bool { repo.mu.Lock(); defer repo.mu.Unlock(); return len(repo.saved) == 1 }, 2*time.Second, time.Millisecond)
	auto.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, int64(1), upstream.calls.Load())
	require.Equal(t, "correct", repo.saved[0].Status)
	require.Equal(t, "automatic", repo.saved[0].Source)
	require.False(t, repo.saved[0].SchedulingStopped)
	require.True(t, repo.saved[0].SchedulingResumed)
	require.True(t, repo.accountsByID[1].Schedulable, "a passing diagnostic restores stopped scheduling")
	require.True(t, repo.next[1].After(time.Now().Add(11*time.Hour)), "stopped accounts use the configured test interval")
}

func TestModelEvaluationAutoRechecksCooldownsBeforeQueueing(t *testing.T) {
	for _, field := range []string{"rate limit", "overload", "temporary pause", "expired"} {
		t.Run(field, func(t *testing.T) {
			upstream := &modelEvaluationAutoUpstream{}
			auto, repo, _ := newModelEvaluationAutoFixture(t, 1, upstream)
			account := repo.accountsByID[1]
			future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
			switch field {
			case "rate limit":
				account.RateLimitResetAt = &future
			case "overload":
				account.OverloadUntil = &future
			case "temporary pause":
				account.TempUnschedulableUntil = &future
			case "expired":
				account.AutoPauseOnExpired, account.ExpiresAt = true, &past
			}
			settings := DefaultModelEvaluationAutoSettings()
			settings.Enabled, settings.Revision = true, "revision-1"
			// The mock deliberately claims even after a new cooldown is observed;
			// the dispatcher must still reject the freshly loaded account.
			require.Zero(t, auto.dispatch(context.Background(), settings, 1))
			require.Empty(t, auto.jobs)
			require.Empty(t, repo.leases)
			require.Zero(t, upstream.calls.Load())
			require.True(t, account.Schedulable, "skipping must not permanently stop scheduling")
			account.RateLimitResetAt, account.OverloadUntil, account.TempUnschedulableUntil = &past, &past, &past
			account.ExpiresAt = &future
			repo.next[1] = time.Time{}
			require.Equal(t, 1, auto.dispatch(context.Background(), settings, 1))
			auto.runJob(<-auto.jobs)
			require.Equal(t, int64(1), upstream.calls.Load())
			require.Len(t, repo.saved, 1)
		})
	}
}

func TestModelEvaluationAutoSettingsDefaultsValidationAndRevision(t *testing.T) {
	settingsService := &SettingService{settingRepo: &upstreamBillingProbeSettingRepo{}}
	settings, err := settingsService.GetModelEvaluationAutoSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 12, settings.IntervalHours)
	require.Equal(t, 3, settings.Concurrency)
	tester := &AccountTestService{settingService: settingsService}
	settings.Enabled = true
	saved, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), settings)
	require.NoError(t, err)
	require.NotEmpty(t, saved.Revision)
	loaded, err := settingsService.GetModelEvaluationAutoSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, saved, loaded)
	for _, change := range []func(*ModelEvaluationAutoSettings){
		func(s *ModelEvaluationAutoSettings) { s.IntervalHours = 0 }, func(s *ModelEvaluationAutoSettings) { s.IntervalHours = 169 },
		func(s *ModelEvaluationAutoSettings) { s.Concurrency = 0 }, func(s *ModelEvaluationAutoSettings) { s.Concurrency = 11 },
	} {
		invalid := settings
		change(&invalid)
		_, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), invalid)
		require.Error(t, err)
	}
}

type modelEvaluationAtomicSettingsRepo struct {
	AccountRepository
	saved *ModelEvaluationAutoSettings
	err   error
}

func (r *modelEvaluationAtomicSettingsRepo) SaveModelEvaluationAutoSettings(_ context.Context, settings ModelEvaluationAutoSettings) error {
	r.saved = &settings
	return r.err
}

func TestModelEvaluationAutoSettingsUseAtomicRepositoryAndOnlyReloadAfterCommit(t *testing.T) {
	settingRepo := &upstreamBillingProbeSettingRepo{}
	repository := &modelEvaluationAtomicSettingsRepo{err: errors.New("reschedule failed")}
	var reloads int
	tester := &AccountTestService{accountRepo: repository, settingService: &SettingService{settingRepo: settingRepo},
		modelEvaluationAuto: &ModelEvaluationAutoService{wake: make(chan struct{}, 1), activeCancel: func() { reloads++ }},
	}
	settings := DefaultModelEvaluationAutoSettings()
	settings.Enabled = true
	_, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), settings)
	require.EqualError(t, err, "reschedule failed")
	require.Zero(t, reloads, "failed transactions must leave running tasks on their existing settings")
	require.Empty(t, settingRepo.values, "must not partially save settings through a second repository")
	repository.err = nil
	updated, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), settings)
	require.NoError(t, err)
	require.Equal(t, updated, *repository.saved)
	require.NotEmpty(t, updated.Revision)
	require.Equal(t, 1, reloads)
	require.Empty(t, settingRepo.values)
}

type modelEvaluationFlakySettingsReader struct {
	base  modelEvaluationAutoSettingsReader
	fail  atomic.Bool
	reads atomic.Int64
}

func (r *modelEvaluationFlakySettingsReader) GetModelEvaluationAutoSettings(ctx context.Context) (ModelEvaluationAutoSettings, error) {
	r.reads.Add(1)
	if r.fail.Load() {
		return DefaultModelEvaluationAutoSettings(), errors.New("temporary settings outage")
	}
	return r.base.GetModelEvaluationAutoSettings(ctx)
}

func TestModelEvaluationAutoTransientSettingsErrorKeepsInFlightWork(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{block: true}
	auto, _, _ := newModelEvaluationAutoFixture(t, 3, upstream)
	reader := &modelEvaluationFlakySettingsReader{base: auto.settings}
	auto.settings = reader
	auto.pollInterval = 5 * time.Millisecond
	auto.Start()
	require.Eventually(t, func() bool { return upstream.active.Load() == 3 }, 2*time.Second, time.Millisecond)
	reader.fail.Store(true)
	before := reader.reads.Load()
	require.Eventually(t, func() bool { return reader.reads.Load() >= before+3 }, 2*time.Second, time.Millisecond)
	require.Equal(t, int64(3), upstream.active.Load(), "a failed settings read must not disable or cancel the last successful revision")
	require.Equal(t, int64(3), upstream.calls.Load())
	auto.Stop()
	require.Zero(t, upstream.active.Load())
}

func TestModelEvaluationAutoFirstSettingsErrorDoesNotStartWork(t *testing.T) {
	upstream := &modelEvaluationAutoUpstream{block: true}
	auto, _, _ := newModelEvaluationAutoFixture(t, 1, upstream)
	reader := &modelEvaluationFlakySettingsReader{base: auto.settings}
	reader.fail.Store(true)
	auto.settings = reader
	auto.pollInterval = 5 * time.Millisecond
	auto.Start()
	require.Eventually(t, func() bool { return reader.reads.Load() >= 3 }, 2*time.Second, time.Millisecond)
	require.Zero(t, upstream.calls.Load())
	reader.fail.Store(false)
	require.Eventually(t, func() bool { return upstream.active.Load() == 1 }, 2*time.Second, time.Millisecond)
	auto.Stop()
}

func TestModelEvaluationAutoDisabledPollingKeepsContextUntilReload(t *testing.T) {
	auto, _, tester := newModelEvaluationAutoFixture(t, 1, &modelEvaluationAutoUpstream{})
	_, err := tester.UpdateModelEvaluationAutoSettings(context.Background(), DefaultModelEvaluationAutoSettings())
	require.NoError(t, err)
	reader := &modelEvaluationFlakySettingsReader{base: auto.settings}
	auto.settings = reader
	auto.pollInterval = 5 * time.Millisecond
	auto.Start()
	require.Eventually(t, func() bool { auto.mu.Lock(); defer auto.mu.Unlock(); return auto.activeCancel != nil }, 2*time.Second, time.Millisecond)
	var cancellations atomic.Int64
	auto.mu.Lock()
	original := auto.activeCancel
	auto.activeCancel = func() { cancellations.Add(1); original() }
	auto.mu.Unlock()
	before := reader.reads.Load()
	require.Eventually(t, func() bool { return reader.reads.Load() >= before+3 }, 2*time.Second, time.Millisecond)
	require.Zero(t, cancellations.Load(), "disabled polling should not repeatedly recreate the run context")
	auto.Reload()
	require.Positive(t, cancellations.Load(), "explicit reload must still cancel the current context")
	auto.Stop()
}

func TestModelEvaluationAutoRejectedClaimPreservesLeaseWindow(t *testing.T) {
	auto, repo, _ := newModelEvaluationAutoFixture(t, 1, &modelEvaluationAutoUpstream{})
	settings := DefaultModelEvaluationAutoSettings()
	settings.Enabled, settings.Revision = true, "stale-revision"
	require.Zero(t, auto.dispatch(context.Background(), settings, 1))
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Empty(t, repo.leases)
	require.Len(t, repo.releaseDue, 1)
	require.True(t, repo.releaseDue[0].IsZero())
	require.True(t, repo.next[1].After(time.Now().Add(modelEvaluationTimeout)))
}

func TestModelEvaluationAutoCancelledQueuePreservesLeaseWindow(t *testing.T) {
	auto, repo, _ := newModelEvaluationAutoFixture(t, 1, &modelEvaluationAutoUpstream{})
	auto.jobs = make(chan modelEvaluationAutoJob)
	settings := DefaultModelEvaluationAutoSettings()
	settings.Enabled, settings.Revision = true, "revision-1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { auto.dispatch(ctx, settings, 1); close(finished) }()
	require.Eventually(t, func() bool { repo.mu.Lock(); defer repo.mu.Unlock(); return repo.batchReads == 1 }, 2*time.Second, time.Millisecond)
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled dispatch did not finish")
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Empty(t, repo.leases)
	require.Len(t, repo.releaseDue, 1)
	require.True(t, repo.releaseDue[0].IsZero())
	require.True(t, repo.next[1].After(time.Now().Add(modelEvaluationTimeout)))
}

func TestModelEvaluationManualMaximumRunsUsesBoundedLease(t *testing.T) {
	_, repo, tester := newModelEvaluationAutoFixture(t, 1, &modelEvaluationAutoUpstream{})
	ctx, recorder := newTestContext()
	originalRequest := ctx.Request
	require.NoError(t, tester.EvaluateOpenAIAccount(ctx, 1, ModelEvaluationRequest{Runs: 10}))
	require.Same(t, originalRequest, ctx.Request)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete","success":true`)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, []time.Duration{102 * time.Minute}, repo.manualLeaseDurations)
	require.LessOrEqual(t, repo.manualLeaseDurations[0], ModelEvaluationMaxLeaseDuration)
	require.Len(t, repo.saved, 10)
}
