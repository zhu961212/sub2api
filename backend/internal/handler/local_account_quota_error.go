package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type localAccountQuotaDiagnoser interface {
	DiagnoseLocalAccountQuota(context.Context, *int64, string, string) service.LocalAccountQuotaState
}

func classifyLocalAccountQuotaFailure(
	ctx context.Context,
	diagnoser service.ModelAvailabilityDiagnoser,
	apiKey *service.APIKey,
	model, platform string,
	fallback noAccountErrorClassification,
) noAccountErrorClassification {
	if fallback.ModelNotFound || apiKey == nil {
		return fallback
	}
	local, ok := diagnoser.(localAccountQuotaDiagnoser)
	if !ok || !local.DiagnoseLocalAccountQuota(ctx, apiKey.GroupID, model, platform).Exhausted {
		return fallback
	}
	return noAccountErrorClassification{
		Status: http.StatusTooManyRequests, ErrType: "rate_limit_error",
		Message: "Local account spending limit reached. Please retry after the quota window resets.",
	}
}
