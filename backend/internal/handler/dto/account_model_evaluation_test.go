package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountListModelEvaluationKeepsVerdictWithoutArtwork(t *testing.T) {
	for _, history := range []any{
		[]service.ModelEvaluationResult{{ID: "latest", Kind: "pelican", Model: "model", Status: "completed", Answer: "full-answer", SVG: "<svg/>"}, {ID: "older"}},
		[]any{map[string]any{"id": "latest", "kind": "pelican", "model": "model", "status": "completed", "answer": "full-answer", "svg": "<svg/>"}, map[string]any{"id": "older"}},
	} {
		account := &Account{Extra: map[string]any{service.ModelEvaluationResultsExtraKey: history, "quota_daily_limit": 100.0}}
		before, err := json.Marshal(account)
		require.NoError(t, err)
		listItem := AccountListItemFromAccount(account)
		raw, err := json.Marshal(listItem)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"id":"latest"`)
		require.Contains(t, string(raw), `"status":"completed"`)
		require.Contains(t, string(raw), `"answer":""`)
		require.NotContains(t, string(raw), "older")
		require.NotContains(t, string(raw), "full-answer")
		require.NotContains(t, string(raw), `"svg"`)
		require.Equal(t, 100.0, listItem.Extra["quota_daily_limit"])
		after, err := json.Marshal(account)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after), "list projection must not mutate the full detail/history")
	}
}

func TestAccountListModelEvaluationKeepsLatestPerKind(t *testing.T) {
	records := []service.ModelEvaluationResult{
		{ID: "pelican-latest", Kind: "pelican", Status: "completed", Answer: "artwork answer", SVG: "<svg/>"},
		{ID: "pelican-older", Kind: "pelican", Status: "failed", Answer: "old artwork"},
		{ID: "invalid-kind", Kind: "other", Answer: "invalid"},
		{ID: "candy-latest", Kind: "candy", Status: "correct", Answer: "21"},
		{ID: "candy-older", Kind: "candy", Status: "wrong", Answer: "20"},
	}
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	var generic []any
	require.NoError(t, json.Unmarshal(raw, &generic))
	maps := make([]map[string]any, 0, len(generic))
	for _, value := range generic {
		record, ok := value.(map[string]any)
		require.True(t, ok)
		maps = append(maps, record)
	}
	for _, history := range []any{records, generic, maps} {
		extra := map[string]any{service.ModelEvaluationResultsExtraKey: history, "quota_daily_limit": 30.0}
		before, err := json.Marshal(extra)
		require.NoError(t, err)
		projected := accountListEvaluationExtra(extra)
		encoded, err := json.Marshal(projected)
		require.NoError(t, err)
		var decoded struct {
			Results []service.ModelEvaluationResult `json:"model_evaluation_results"`
		}
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		require.Len(t, decoded.Results, 2)
		require.Equal(t, "pelican-latest", decoded.Results[0].ID)
		require.Equal(t, "candy-latest", decoded.Results[1].ID)
		for _, result := range decoded.Results {
			require.Empty(t, result.Answer)
			require.Empty(t, result.SVG)
		}
		require.Equal(t, 30.0, projected["quota_daily_limit"])
		after, err := json.Marshal(extra)
		require.NoError(t, err)
		require.JSONEq(t, string(before), string(after))
	}
}
