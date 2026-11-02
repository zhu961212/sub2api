package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type localQuotaFailureDiagnoser struct {
	service.ModelAvailabilityDiagnoser
	state service.LocalAccountQuotaState
	calls int
}

func (d *localQuotaFailureDiagnoser) DiagnoseLocalAccountQuota(context.Context, *int64, string, string) service.LocalAccountQuotaState {
	d.calls++
	return d.state
}

func TestLocalAccountQuotaFailureReturns429WithoutMaskingModelErrors(t *testing.T) {
	diagnoser := &localQuotaFailureDiagnoser{state: service.LocalAccountQuotaState{Exhausted: true}}
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error"}
	result := classifyLocalAccountQuotaFailure(context.Background(), diagnoser, &service.APIKey{}, "gpt-6.1-sol", service.PlatformOpenAI, fallback)
	require.Equal(t, http.StatusTooManyRequests, result.Status)
	require.Equal(t, "rate_limit_error", result.ErrType)
	require.Contains(t, result.Message, "spending limit")

	notFound := noAccountErrorClassification{Status: http.StatusNotFound, ModelNotFound: true}
	require.Equal(t, notFound, classifyLocalAccountQuotaFailure(context.Background(), diagnoser, &service.APIKey{}, "missing", service.PlatformOpenAI, notFound))
	require.Equal(t, 1, diagnoser.calls)
	diagnoser.state.Exhausted = false
	require.Equal(t, fallback, classifyLocalAccountQuotaFailure(context.Background(), diagnoser, &service.APIKey{}, "gpt-6.1-sol", service.PlatformOpenAI, fallback))
}
