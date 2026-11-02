package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const SettingKeyModelEvaluationAutoSettings = "model_evaluation_auto_settings"
const modelEvaluationMaxWorkers = 10

type ModelEvaluationAutoSettings struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"interval_hours"`
	Concurrency   int    `json:"concurrency"`
	ModelID       string `json:"model_id"`
	Effort        string `json:"effort"`
	Revision      string `json:"revision"`
}

func DefaultModelEvaluationAutoSettings() ModelEvaluationAutoSettings {
	return ModelEvaluationAutoSettings{IntervalHours: 12, Concurrency: 3, Effort: "low"}
}

func (settings *ModelEvaluationAutoSettings) Validate() error {
	if settings.IntervalHours < 1 || settings.IntervalHours > 168 {
		return infraerrors.BadRequest("INVALID_MODEL_EVALUATION_INTERVAL", "interval_hours must be between 1 and 168")
	}
	if settings.Concurrency < 1 || settings.Concurrency > modelEvaluationMaxWorkers {
		return infraerrors.BadRequest("INVALID_MODEL_EVALUATION_CONCURRENCY", "concurrency must be between 1 and 10")
	}
	req, err := normalizeModelEvaluationRequest(ModelEvaluationRequest{Kind: "candy", ModelID: settings.ModelID, Effort: settings.Effort, Runs: 1})
	if err != nil {
		return err
	}
	settings.ModelID = strings.TrimSpace(settings.ModelID)
	settings.Effort = req.Effort
	return nil
}

func (s *SettingService) GetModelEvaluationAutoSettings(ctx context.Context) (ModelEvaluationAutoSettings, error) {
	settings := DefaultModelEvaluationAutoSettings()
	if s == nil || s.settingRepo == nil {
		return settings, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyModelEvaluationAutoSettings)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(raw) == "") {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return DefaultModelEvaluationAutoSettings(), err
	}
	return settings, settings.Validate()
}

func (s *AccountTestService) GetModelEvaluationAutoSettings(ctx context.Context) (ModelEvaluationAutoSettings, error) {
	return s.settingService.GetModelEvaluationAutoSettings(ctx)
}

func (s *AccountTestService) UpdateModelEvaluationAutoSettings(ctx context.Context, settings ModelEvaluationAutoSettings) (ModelEvaluationAutoSettings, error) {
	if err := settings.Validate(); err != nil {
		return settings, err
	}
	if s.settingService == nil || s.settingService.settingRepo == nil {
		return settings, infraerrors.ServiceUnavailable("MODEL_EVALUATION_UNAVAILABLE", "Model evaluation settings are unavailable")
	}
	settings.Revision = uuid.NewString()
	// Commit the new revision before cancelling local work. Repository completion
	// checks this same row under lock, so another instance cannot apply an old
	// wrong answer after automatic evaluation has been disabled or reconfigured.
	if repository, ok := s.accountRepo.(ModelEvaluationSettingsRepository); ok {
		if err := repository.SaveModelEvaluationAutoSettings(ctx, settings); err != nil {
			return settings, err
		}
	} else {
		// Compatibility for isolated service integrations without account storage.
		// The production account repository atomically re-times pending jobs too.
		raw, err := json.Marshal(settings)
		if err != nil {
			return settings, err
		}
		if err := s.settingService.settingRepo.Set(ctx, SettingKeyModelEvaluationAutoSettings, string(raw)); err != nil {
			return settings, err
		}
	}
	if s.modelEvaluationAuto != nil {
		s.modelEvaluationAuto.Reload()
	}
	return settings, nil
}

type modelEvaluationAutoSettingsReader interface {
	GetModelEvaluationAutoSettings(context.Context) (ModelEvaluationAutoSettings, error)
}

type modelEvaluationAutoJob struct {
	ctx      context.Context
	lease    ModelEvaluationLease
	account  *Account
	settings ModelEvaluationAutoSettings
}

// ModelEvaluationAutoService uses a fixed-size worker pool and claims only the
// available slots from an indexed due query. It never scans account histories
// or creates a goroutine per account, and PostgreSQL leases coordinate replicas.
type ModelEvaluationAutoService struct {
	repo         ModelEvaluationRepository
	tester       *AccountTestService
	settings     modelEvaluationAutoSettingsReader
	ctx          context.Context
	cancel       context.CancelFunc
	wake         chan struct{}
	jobs         chan modelEvaluationAutoJob
	finished     chan struct{}
	start        sync.Once
	stop         sync.Once
	wg           sync.WaitGroup
	mu           sync.Mutex
	activeCancel context.CancelFunc
	pollInterval time.Duration
}

func NewModelEvaluationAutoService(repository ModelEvaluationRepository, tester *AccountTestService, settings *SettingService) *ModelEvaluationAutoService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ModelEvaluationAutoService{repo: repository, tester: tester, settings: settings, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), jobs: make(chan modelEvaluationAutoJob, modelEvaluationMaxWorkers),
		finished: make(chan struct{}, modelEvaluationMaxWorkers), pollInterval: 15 * time.Second}
}

func ProvideModelEvaluationAutoService(repository AccountRepository, tester *AccountTestService, settings *SettingService) *ModelEvaluationAutoService {
	repo, ok := repository.(ModelEvaluationRepository)
	if !ok || tester == nil {
		return nil
	}
	service := NewModelEvaluationAutoService(repo, tester, settings)
	tester.modelEvaluationAuto = service
	return service
}

func (s *ModelEvaluationAutoService) Start() {
	if s == nil || s.repo == nil || s.tester == nil || s.settings == nil {
		return
	}
	s.start.Do(func() {
		for range modelEvaluationMaxWorkers {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				for job := range s.jobs {
					s.runJob(job)
					select {
					case s.finished <- struct{}{}:
					case <-s.ctx.Done():
					}
				}
			}()
		}
		s.wg.Add(1)
		go s.runDispatcher()
	})
}

func (s *ModelEvaluationAutoService) Reload() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ModelEvaluationAutoService) Stop() {
	if s == nil {
		return
	}
	s.stop.Do(func() { s.cancel(); s.Reload(); s.wg.Wait() })
}

func (s *ModelEvaluationAutoService) runDispatcher() {
	defer s.wg.Done()
	defer close(s.jobs)
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	active := 0
	var revision string
	var settings ModelEvaluationAutoSettings
	var runCtx context.Context
	for {
		if s.ctx.Err() != nil {
			return
		}
		loadCtx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		loadedSettings, err := s.settings.GetModelEvaluationAutoSettings(loadCtx)
		cancel()
		if err != nil {
			slog.Warn("model evaluation settings unavailable")
		} else {
			// A transient settings read failure is not a disable operation. Keep
			// in-flight work on the last successful revision; before the first
			// successful read, the zero settings value does not start any jobs.
			settings = loadedSettings
		}
		if runCtx == nil || runCtx.Err() != nil || settings.Revision != revision {
			s.mu.Lock()
			if s.activeCancel != nil {
				s.activeCancel()
			}
			runCtx, s.activeCancel = context.WithCancel(s.ctx)
			s.mu.Unlock()
			revision = settings.Revision
		}
		if settings.Enabled && settings.Concurrency > active {
			active += s.dispatch(runCtx, settings, settings.Concurrency-active)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		case <-s.finished:
			active--
		}
	}
}

func (s *ModelEvaluationAutoService) dispatch(ctx context.Context, settings ModelEvaluationAutoSettings, available int) int {
	claimCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	leases, err := s.repo.ClaimDueModelEvaluations(claimCtx, time.Now(), modelEvaluationLeaseDuration(1, modelEvaluationTimeout), min(available, modelEvaluationMaxWorkers))
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("model evaluation due claim failed")
		}
		return 0
	}
	if len(leases) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(leases))
	for _, lease := range leases {
		ids = append(ids, lease.AccountID)
	}
	accounts, err := s.repo.GetModelEvaluationAccounts(claimCtx, ids)
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	started := 0
	for _, lease := range leases {
		account := byID[lease.AccountID]
		eligible := automaticModelEvaluationAccountEligible(account, time.Now())
		if err != nil || !eligible || lease.Revision != settings.Revision || ctx.Err() != nil {
			releaseModelEvaluationLease(s.repo, lease, time.Time{})
			continue
		}
		select {
		case s.jobs <- modelEvaluationAutoJob{ctx: ctx, lease: lease, account: account, settings: settings}:
			started++
		case <-ctx.Done():
			releaseModelEvaluationLease(s.repo, lease, time.Time{})
		}
	}
	return started
}

func automaticModelEvaluationAccountEligible(account *Account, now time.Time) bool {
	// Diagnostics also cover accounts whose scheduling was stopped so recovery
	// can be observed without putting them back into the gateway pool.
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() || account.Status != StatusActive || account.IsSyntheticUITest() {
		return false
	}
	for _, until := range []*time.Time{account.RateLimitResetAt, account.OverloadUntil, account.TempUnschedulableUntil} {
		if until != nil && now.Before(*until) {
			return false
		}
	}
	return !account.AutoPauseOnExpired || account.ExpiresAt == nil || now.Before(*account.ExpiresAt)
}

func (s *ModelEvaluationAutoService) runJob(job modelEvaluationAutoJob) {
	defer releaseModelEvaluationLease(s.repo, job.lease, time.Time{})
	if job.ctx.Err() != nil {
		return
	}
	if _, running := s.tester.modelEvaluationRuns.LoadOrStore(job.account.ID, struct{}{}); running {
		return
	}
	defer s.tester.modelEvaluationRuns.Delete(job.account.ID)
	result, err := s.tester.runModelEvaluation(job.ctx, job.account, ModelEvaluationRequest{
		Kind: "candy", ModelID: job.settings.ModelID, Effort: job.settings.Effort, Runs: 1,
	}, "automatic", 1, nil)
	if err != nil || job.ctx.Err() != nil {
		return
	}
	_, err = s.repo.SaveModelEvaluationResult(job.ctx, job.lease, &result,
		time.Now().Add(time.Duration(job.settings.IntervalHours)*time.Hour), true)
	if err != nil && job.ctx.Err() == nil {
		slog.Warn("automatic model evaluation result could not be saved", "account_id", job.account.ID)
	}
}
