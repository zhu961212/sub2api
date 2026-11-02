package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

// Keep each test's latest verdict so switching tabs does not mix candy and
// pelican results. Full answers and SVGs stay in the bounded history endpoint.
func accountListEvaluationExtra(extra map[string]any) map[string]any {
	history, exists := extra[service.ModelEvaluationResultsExtraKey]
	if !exists {
		return extra
	}
	projected := make(map[string]any, len(extra))
	for key, value := range extra {
		projected[key] = value
	}
	delete(projected, service.ModelEvaluationResultsExtraKey)
	seen := make(map[string]bool, 2)
	summaries := make([]any, 0, 2)
	appendSummary := func(record map[string]any) {
		kind, _ := record["kind"].(string)
		if (kind != "candy" && kind != "pelican") || seen[kind] {
			return
		}
		seen[kind] = true
		summary := make(map[string]any, len(record))
		for key, value := range record {
			if key != "answer" && key != "svg" {
				summary[key] = value
			}
		}
		summary["answer"] = ""
		summaries = append(summaries, summary)
	}
	switch records := history.(type) {
	case []service.ModelEvaluationResult:
		for _, result := range records {
			if (result.Kind != "candy" && result.Kind != "pelican") || seen[result.Kind] {
				continue
			}
			seen[result.Kind] = true
			result.Answer, result.SVG = "", ""
			summaries = append(summaries, result)
		}
	case []any:
		for _, record := range records {
			if fields, ok := record.(map[string]any); ok {
				appendSummary(fields)
			}
		}
	case []map[string]any:
		for _, record := range records {
			appendSummary(record)
		}
	}
	if len(summaries) > 0 {
		projected[service.ModelEvaluationResultsExtraKey] = summaries
	}
	return projected
}
