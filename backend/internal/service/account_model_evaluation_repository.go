package service

import (
	"context"
	"time"
)

const ModelEvaluationPauseExtraKey = "model_evaluation_pause"

// ModelEvaluationMaxLeaseDuration is shared by request budgeting and database
// validation. A future larger test budget may outlive this lease; completions
// remain fenced by ownership rather than extending the repository limit.
const ModelEvaluationMaxLeaseDuration = 2 * time.Hour

func modelEvaluationLeaseDuration(runs int, timeout time.Duration) time.Duration {
	if runs <= 0 || timeout <= 0 {
		return 0
	}
	const grace = 2 * time.Minute
	budget := ModelEvaluationMaxLeaseDuration - grace
	// Check before multiplying so a future larger budget cannot overflow.
	if int64(runs) > int64(budget/timeout) {
		return ModelEvaluationMaxLeaseDuration
	}
	return time.Duration(runs)*timeout + grace
}

// ModelEvaluationLease is an ownership token, not an account credential. The
// repository fences every completion and release against the current token.
type ModelEvaluationLease struct {
	AccountID int64
	Token     string
	Automatic bool
	Revision  string
}

// ModelEvaluationRepository is deliberately separate from AccountRepository so
// ordinary account reads do not carry background job state or large histories.
type ModelEvaluationRepository interface {
	ClaimDueModelEvaluations(ctx context.Context, now time.Time, leaseDuration time.Duration, limit int) ([]ModelEvaluationLease, error)
	ClaimModelEvaluation(ctx context.Context, accountID int64, now time.Time, leaseDuration time.Duration) (*ModelEvaluationLease, error)
	GetModelEvaluationAccounts(ctx context.Context, ids []int64) ([]*Account, error)
	SaveModelEvaluationResult(ctx context.Context, lease ModelEvaluationLease, result *ModelEvaluationResult, nextDue time.Time, release bool) (bool, error)
	ReleaseModelEvaluation(ctx context.Context, lease ModelEvaluationLease, nextDue time.Time) error
}

// Settings writes also reschedule future jobs when the interval changes.
type ModelEvaluationSettingsRepository interface {
	SaveModelEvaluationAutoSettings(ctx context.Context, settings ModelEvaluationAutoSettings) error
}
