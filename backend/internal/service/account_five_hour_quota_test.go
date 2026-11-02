package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountFiveHourQuotaSchedulingAndIndependentDailyLimit(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name         string
		start        time.Time
		used, limit  float64
		dailyUsed    float64
		wantExceeded bool
	}{
		{name: "five_hour_limit_reached", start: now.Add(-time.Hour), used: 5, limit: 5, wantExceeded: true},
		{name: "below_limit", start: now.Add(-time.Hour), used: 4, limit: 5},
		{name: "expired_window", start: now.Add(-5*time.Hour - time.Second), used: 8, limit: 5},
		{name: "exact_window_boundary", start: now.Add(-5 * time.Hour), used: 8, limit: 5},
		{name: "never_started", used: 8, limit: 5},
		{name: "disabled_limit", start: now.Add(-time.Hour), used: 8},
		{name: "daily_limit_still_blocks_after_five_hours", start: now.Add(-6 * time.Hour), used: 8, limit: 5, dailyUsed: 20, wantExceeded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
				Extra: map[string]any{"quota_5h_limit": tc.limit, "quota_5h_used": tc.used,
					"quota_daily_limit": 20.0, "quota_daily_used": tc.dailyUsed, "quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339Nano)}}
			if !tc.start.IsZero() {
				account.Extra["quota_5h_start"] = tc.start.Format(time.RFC3339Nano)
			}
			require.Equal(t, tc.wantExceeded, account.IsQuotaExceeded())
			require.Equal(t, !tc.wantExceeded, account.IsSchedulable())
			delete(account.Extra, "quota_daily_limit")
			require.Equal(t, tc.limit > 0, account.HasAnyQuotaLimit())
			if tc.start.IsZero() {
				require.True(t, account.Quota5hResetAt().IsZero())
			} else {
				require.Equal(t, tc.start.Add(5*time.Hour), account.Quota5hResetAt())
			}
		})
	}
}

func TestAccountFiveHourQuotaBlocksParentAndShadowUntilPeriodExpires(t *testing.T) {
	parent := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{"quota_5h_limit": 5.0, "quota_5h_used": 5.0, "quota_5h_start": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}}
	shadow := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Status: StatusActive, Schedulable: true}
	lookup := func(id int64) *Account { require.Equal(t, parent.ID, id); return parent }
	require.False(t, parent.IsSchedulable())
	require.False(t, parentHealthyForShadow(shadow, lookup))
	parent.Extra["quota_5h_start"] = time.Now().Add(-6 * time.Hour).Format(time.RFC3339Nano)
	require.True(t, parent.IsSchedulable())
	require.True(t, parentHealthyForShadow(shadow, lookup))
}
