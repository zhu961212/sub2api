package service

import "context"

type accountListEvaluationSummariesKey struct{}

// WithAccountListEvaluationSummaries requests compact evaluation metadata on
// paginated admin list reads. Detail, export and scheduler reads remain full.
func WithAccountListEvaluationSummaries(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountListEvaluationSummariesKey{}, true)
}

func AccountListEvaluationSummariesRequested(ctx context.Context) bool {
	requested, _ := ctx.Value(accountListEvaluationSummariesKey{}).(bool)
	return requested
}
