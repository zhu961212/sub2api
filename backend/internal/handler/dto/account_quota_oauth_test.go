package dto

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIOAuthLocalQuotaDTOResetsExpiredUsage(t *testing.T) {
	account := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 20.0, "quota_daily_used": 20.0, "quota_daily_start": time.Now().Add(-25 * time.Hour).Format(time.RFC3339)}}
	out := AccountFromServiceShallow(account)
	require.NotNil(t, out.QuotaDailyLimit)
	require.Equal(t, 20.0, *out.QuotaDailyLimit)
	require.NotNil(t, out.QuotaDailyUsed)
	require.Zero(t, *out.QuotaDailyUsed)
	item := AccountListItemFromAccount(out)
	require.Equal(t, out.QuotaDailyLimit, item.QuotaDailyLimit)
	account.Platform = service.PlatformAnthropic
	require.Nil(t, AccountFromServiceShallow(account).QuotaDailyLimit)
}
