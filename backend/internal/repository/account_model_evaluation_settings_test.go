package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestModelEvaluationSettingsRescheduleAtomically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hours int
		fail  bool
	}{
		{"shorter interval", 6, false}, {"longer interval", 24, false}, {"unchanged interval", 12, false}, {"reschedule failure", 6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := modelEvaluationJobRepo(t)
			settings := service.DefaultModelEvaluationAutoSettings()
			settings.Enabled, settings.Revision, settings.IntervalHours = true, "new-revision", tc.hours
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyModelEvaluationAutoSettings, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery("SELECT value FROM settings WHERE key = \\$1 FOR UPDATE").WithArgs(service.SettingKeyModelEvaluationAutoSettings).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"enabled":true,"interval_hours":12,"concurrency":3,"effort":"low","revision":"old"}`))
			mock.ExpectExec("UPDATE settings SET value").WithArgs(service.SettingKeyModelEvaluationAutoSettings, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			if tc.hours != 12 {
				update := mock.ExpectExec(regexp.QuoteMeta(rescheduleModelEvaluationsSQL)).WithArgs(tc.hours - 12)
				if tc.fail {
					update.WillReturnError(errors.New("reschedule unavailable"))
				} else {
					update.WillReturnResult(sqlmock.NewResult(0, 7))
				}
			}
			if tc.fail {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err := repo.SaveModelEvaluationAutoSettings(context.Background(), settings)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	require.Contains(t, rescheduleModelEvaluationsSQL, "GREATEST(NOW()")
	require.Contains(t, rescheduleModelEvaluationsSQL, "candy_evaluation_next_run_at > NOW()")
	require.Contains(t, rescheduleModelEvaluationsSQL, "model_evaluation_lease_until <= NOW()")
	require.Contains(t, rescheduleModelEvaluationsSQL, "parent_account_id IS NULL")
}
