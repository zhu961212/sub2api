package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const ModelEvaluationHistoryBatchLimit = 20

// This projection intentionally cannot contain OAuth credentials, proxy
// passwords, group memberships, or unrelated account extra fields.
type ModelEvaluationHistoryAccount struct {
	ID           int64                   `json:"id"`
	Name         string                  `json:"name"`
	Platform     string                  `json:"platform"`
	Type         string                  `json:"type"`
	Schedulable  bool                    `json:"schedulable"`
	ProxyID      *int64                  `json:"proxy_id"`
	ProxyName    *string                 `json:"proxy_name"`
	Results      []ModelEvaluationResult `json:"results"`
	HistoryError string                  `json:"history_error,omitempty"`
}

type ModelEvaluationHistoryBatch struct {
	Accounts   []ModelEvaluationHistoryAccount `json:"accounts"`
	MissingIDs []int64                         `json:"missing_ids"`
}

type ModelEvaluationHistoryRepository interface {
	GetModelEvaluationHistory(ctx context.Context, ids []int64) (*ModelEvaluationHistoryBatch, error)
}

type ModelEvaluationHistoryReader interface {
	GetModelEvaluationHistory(ctx context.Context, ids []int64) (*ModelEvaluationHistoryBatch, error)
}

func ValidateModelEvaluationHistoryIDs(ids []int64) error {
	if len(ids) < 1 || len(ids) > ModelEvaluationHistoryBatchLimit {
		return infraerrors.BadRequest("INVALID_EVALUATION_HISTORY_IDS", "Request between 1 and 20 unique account IDs")
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return infraerrors.BadRequest("INVALID_EVALUATION_HISTORY_IDS", "Account IDs must be positive integers")
		}
		if _, exists := seen[id]; exists {
			return infraerrors.BadRequest("INVALID_EVALUATION_HISTORY_IDS", "Duplicate account IDs are not allowed")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *adminServiceImpl) GetModelEvaluationHistory(ctx context.Context, ids []int64) (*ModelEvaluationHistoryBatch, error) {
	if adminaccess.FromContext(ctx) != nil {
		return nil, infraerrors.Forbidden("MODEL_EVALUATION_HISTORY_FORBIDDEN", "Model evaluation history requires full administrator access")
	}
	if err := ValidateModelEvaluationHistoryIDs(ids); err != nil {
		return nil, err
	}
	repo, ok := s.accountRepo.(ModelEvaluationHistoryRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("MODEL_EVALUATION_HISTORY_UNAVAILABLE", "Model evaluation history is unavailable")
	}
	return repo.GetModelEvaluationHistory(ctx, ids)
}
