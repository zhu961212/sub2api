package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFiveHourQuotaDTOAndLiteListAgree(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name      string
		start     time.Time
		wantUsed  float64
		wantReset bool
	}{
		{name: "active", start: now.Add(-time.Hour), wantUsed: 4, wantReset: true},
		{name: "expired", start: now.Add(-6 * time.Hour)},
		{name: "never_started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Extra: map[string]any{"quota_5h_limit": 5.0, "quota_5h_used": 4.0}}
			if !tc.start.IsZero() {
				account.Extra["quota_5h_start"] = tc.start.Format(time.RFC3339)
			}
			out := AccountFromServiceShallow(account)
			require.NotNil(t, out.Quota5hLimit)
			require.Equal(t, 5.0, *out.Quota5hLimit)
			require.NotNil(t, out.Quota5hUsed)
			require.Equal(t, tc.wantUsed, *out.Quota5hUsed)
			if tc.wantReset {
				require.Equal(t, tc.start.Add(5*time.Hour).Format(time.RFC3339), *out.Quota5hResetAt)
			} else {
				require.Nil(t, out.Quota5hResetAt)
			}
			item := AccountListItemFromAccount(out)
			require.Equal(t, out.Quota5hLimit, item.Quota5hLimit)
			require.Equal(t, out.Quota5hUsed, item.Quota5hUsed)
			require.Equal(t, out.Quota5hResetAt, item.Quota5hResetAt)
			account.Platform = service.PlatformAnthropic
			require.Nil(t, AccountFromServiceShallow(account).Quota5hLimit)
		})
	}
}
