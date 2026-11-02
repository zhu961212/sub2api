package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type evaluationHistoryRepositoryStub struct {
	AccountRepository
	calls int
	ids   []int64
}

func (s *evaluationHistoryRepositoryStub) GetModelEvaluationHistory(_ context.Context, ids []int64) (*ModelEvaluationHistoryBatch, error) {
	s.calls++
	s.ids = append([]int64(nil), ids...)
	return &ModelEvaluationHistoryBatch{Accounts: []ModelEvaluationHistoryAccount{}, MissingIDs: ids}, nil
}

func TestModelEvaluationHistoryServiceValidatesIDsAndAuthorization(t *testing.T) {
	for _, ids := range [][]int64{nil, {}, {0}, {-1}, {1, 1}, make([]int64, 21)} {
		repo := &evaluationHistoryRepositoryStub{}
		svc := &adminServiceImpl{accountRepo: repo}
		_, err := svc.GetModelEvaluationHistory(context.Background(), ids)
		require.Equal(t, 400, infraerrors.Code(err))
		require.Zero(t, repo.calls)
	}
	repo := &evaluationHistoryRepositoryStub{}
	svc := &adminServiceImpl{accountRepo: repo}
	ctx := adminaccess.WithPrincipal(context.Background(), &adminaccess.Principal{KeyID: "restricted"})
	_, err := svc.GetModelEvaluationHistory(ctx, []int64{1})
	require.Equal(t, 403, infraerrors.Code(err))
	require.Zero(t, repo.calls)
	ids := make([]int64, 20)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	batch, err := svc.GetModelEvaluationHistory(context.Background(), ids)
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.Equal(t, ids, repo.ids)
	require.Equal(t, ids, batch.MissingIDs)
}
