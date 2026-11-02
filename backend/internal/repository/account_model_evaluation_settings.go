package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const rescheduleModelEvaluationsSQL = `UPDATE accounts
	SET candy_evaluation_next_run_at = GREATEST(NOW(), candy_evaluation_next_run_at + $1 * INTERVAL '1 hour')
	WHERE deleted_at IS NULL AND platform = 'openai' AND type = 'oauth' AND parent_account_id IS NULL
	AND candy_evaluation_next_run_at > NOW()
	AND (model_evaluation_lease_until IS NULL OR model_evaluation_lease_until <= NOW())`

func (r *accountRepository) SaveModelEvaluationAutoSettings(ctx context.Context, settings service.ModelEvaluationAutoSettings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(settings.Revision) == "" {
		return errors.New("model evaluation settings revision is required")
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	defaults := service.DefaultModelEvaluationAutoSettings()
	initial, err := json.Marshal(defaults)
	if err != nil {
		return err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	db := tx.Client()
	// Ensure a lockable row even for the first configuration. Concurrent saves,
	// claims, and completions then all follow the same settings-before-account order.
	if _, err := db.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at) VALUES($1, $2, NOW()) ON CONFLICT(key) DO NOTHING`, service.SettingKeyModelEvaluationAutoSettings, string(initial)); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT value FROM settings WHERE key = $1 FOR UPDATE`, service.SettingKeyModelEvaluationAutoSettings)
	if err != nil {
		return err
	}
	var raw string
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		return errors.New("model evaluation settings row unavailable")
	}
	if err := rows.Scan(&raw); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	previous := defaults
	if err := json.Unmarshal([]byte(raw), &previous); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE settings SET value = $2, updated_at = NOW() WHERE key = $1`, service.SettingKeyModelEvaluationAutoSettings, string(payload)); err != nil {
		return err
	}
	if previous.IntervalHours != settings.IntervalHours {
		if _, err := db.ExecContext(ctx, rescheduleModelEvaluationsSQL, settings.IntervalHours-previous.IntervalHours); err != nil {
			return err
		}
	}
	return tx.Commit()
}
