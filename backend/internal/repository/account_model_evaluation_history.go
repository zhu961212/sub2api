package repository

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const modelEvaluationHistoryQuery = `SELECT a.id, a.name, a.platform, a.type, a.schedulable,
	CASE WHEN a.parent_account_id IS NULL THEN a.proxy_id ELSE parent.proxy_id END AS proxy_id,
	p.name AS proxy_name,
	CASE WHEN jsonb_typeof(a.extra->'model_evaluation_results') = 'array' THEN (
		SELECT COALESCE(jsonb_agg(recent.value ORDER BY recent.ordinality), '[]'::jsonb)
		FROM (SELECT value, ordinality FROM jsonb_array_elements(a.extra->'model_evaluation_results')
			WITH ORDINALITY h(value, ordinality) ORDER BY ordinality LIMIT 5) recent
	) ELSE a.extra->'model_evaluation_results' END AS results
FROM accounts a
LEFT JOIN accounts parent ON parent.id = a.parent_account_id AND parent.deleted_at IS NULL
	AND parent.platform = 'openai' AND parent.type = 'oauth' AND parent.parent_account_id IS NULL
LEFT JOIN proxies p ON p.id = CASE WHEN a.parent_account_id IS NULL THEN a.proxy_id ELSE parent.proxy_id END
	AND p.deleted_at IS NULL
WHERE a.id = ANY($1) AND a.deleted_at IS NULL
	AND (a.parent_account_id IS NULL OR parent.id IS NOT NULL)`

func (r *accountRepository) GetModelEvaluationHistory(ctx context.Context, ids []int64) (*service.ModelEvaluationHistoryBatch, error) {
	if err := service.ValidateModelEvaluationHistoryIDs(ids); err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, modelEvaluationHistoryQuery, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byID := make(map[int64]service.ModelEvaluationHistoryAccount, len(ids))
	for rows.Next() {
		var account service.ModelEvaluationHistoryAccount
		var history []byte
		if err := rows.Scan(&account.ID, &account.Name, &account.Platform, &account.Type, &account.Schedulable,
			&account.ProxyID, &account.ProxyName, &history); err != nil {
			return nil, err
		}
		account.Results, account.HistoryError = decodeModelEvaluationHistory(history)
		byID[account.ID] = account
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	batch := &service.ModelEvaluationHistoryBatch{
		Accounts:   make([]service.ModelEvaluationHistoryAccount, 0, len(byID)),
		MissingIDs: make([]int64, 0),
	}
	for _, id := range ids {
		if account, found := byID[id]; found {
			batch.Accounts = append(batch.Accounts, account)
		} else {
			batch.MissingIDs = append(batch.MissingIDs, id)
		}
	}
	return batch, nil
}

func decodeModelEvaluationHistory(raw []byte) ([]service.ModelEvaluationResult, string) {
	results := make([]service.ModelEvaluationResult, 0, service.ModelEvaluationHistoryLimit)
	if len(raw) == 0 || string(raw) == "null" {
		return results, ""
	}
	var records []json.RawMessage
	if err := json.Unmarshal(raw, &records); err != nil {
		return results, "invalid_history"
	}
	if len(records) > service.ModelEvaluationHistoryLimit {
		records = records[:service.ModelEvaluationHistoryLimit]
	}
	bytes := 2
	historyError := ""
	for _, record := range records {
		var result service.ModelEvaluationResult
		if err := json.Unmarshal(record, &result); err != nil || !validModelEvaluationHistoryResult(result) {
			historyError = "invalid_history"
			continue
		}
		// Typed decoding discards unknown fields; do not pass through arbitrary
		// account extra data that happens to be stored beside a historical answer.
		encoded, err := json.Marshal(result)
		if err != nil {
			historyError = "invalid_history"
			continue
		}
		if len(results) > 0 && bytes+len(encoded)+1 > 1<<20 {
			break
		}
		results = append(results, result)
		bytes += len(encoded) + 1
	}
	return results, historyError
}

func validModelEvaluationHistoryResult(result service.ModelEvaluationResult) bool {
	if strings.TrimSpace(result.ID) == "" || result.Model == "" || result.Effort == "" || result.TestedAt == "" {
		return false
	}
	if result.Kind != "candy" && result.Kind != "pelican" {
		return false
	}
	switch result.Status {
	case "correct", "wrong", "completed", "failed":
		return true
	default:
		return false
	}
}
