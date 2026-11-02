package admin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type usageQuotaAdminStub struct {
	service.AdminService
	account *service.Account
	err     error
	readID  int64
}

func (s *usageQuotaAdminStub) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	s.readID = id
	return s.account, s.err
}

func usageQuotaTestAccount() *service.Account {
	now := time.Now().UTC()
	return &service.Account{
		ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		UpdatedAt: now, Schedulable: false,
		Credentials: map[string]any{"access_token": "must-not-be-returned"},
		Extra: map[string]any{
			"quota_limit": 100.0, "quota_used": 20.0,
			"quota_5h_limit": 10.0, "quota_5h_used": 2.0, "quota_5h_start": now.Format(time.RFC3339),
			"quota_daily_limit": 30.0, "quota_daily_used": 4.0, "quota_daily_start": now.Format(time.RFC3339),
			"quota_weekly_limit": 80.0, "quota_weekly_used": 9.0, "quota_weekly_start": now.Format(time.RFC3339),
			"codex_5h_used_percent": 60.0,
		},
	}
}

func TestUsageWithLocalQuotaPreservesUpstreamResponseAndReadsCurrentLedger(t *testing.T) {
	account := usageQuotaTestAccount()
	stub := &usageQuotaAdminStub{account: account}
	handler := &AccountHandler{adminService: stub}
	usage := &service.UsageInfo{Source: "active"}
	result := handler.usageWithLocalQuota(context.Background(), account.ID, usage)
	require.Equal(t, account.ID, stub.readID)
	require.Same(t, usage, result.UsageInfo)
	require.Equal(t, 4.0, result.LocalQuota.QuotaDailyUsed)
	require.Equal(t, 2.0, result.LocalQuota.Quota5hUsed)
	require.Equal(t, account.UpdatedAt, result.LocalQuota.UpdatedAt)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"source":"active"`)
	require.Contains(t, string(raw), `"local_quota":`)
	require.NotContains(t, string(raw), "must-not-be-returned")
	require.NotContains(t, string(raw), "credentials")
	require.NotContains(t, string(raw), "schedulable")
	require.False(t, account.Schedulable)
	require.Equal(t, 4.0, account.Extra["quota_daily_used"])
}

func TestLocalQuotaSnapshotResetsExpiredWindowsWithoutWritingTheLedger(t *testing.T) {
	account := usageQuotaTestAccount()
	account.Extra["quota_5h_start"] = time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339)
	account.Extra["quota_daily_start"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	account.Extra["quota_weekly_start"] = time.Now().Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339)
	snapshot := newAccountLocalQuotaSnapshot(account)
	require.Zero(t, snapshot.Quota5hUsed)
	require.Empty(t, snapshot.Quota5hResetAt)
	require.Zero(t, snapshot.QuotaDailyUsed)
	require.Zero(t, snapshot.QuotaWeeklyUsed)
	require.Equal(t, 20.0, snapshot.QuotaUsed)
	require.Equal(t, 4.0, account.Extra["quota_daily_used"])
}

func TestUsageWithLocalQuotaDoesNotInventZerosWhenLocalReadFails(t *testing.T) {
	handler := &AccountHandler{adminService: &usageQuotaAdminStub{err: errors.New("database unavailable")}}
	usage := &service.UsageInfo{Source: "active"}
	result := handler.usageWithLocalQuota(context.Background(), 42, usage)
	require.Nil(t, result.LocalQuota)
	require.Same(t, usage, result.UsageInfo)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "local_quota")
}

func TestLocalQuotaSnapshotSkipsUnsupportedAndShadowAccounts(t *testing.T) {
	require.Nil(t, newAccountLocalQuotaSnapshot(nil))
	require.Nil(t, newAccountLocalQuotaSnapshot(&service.Account{Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth}))
	account := usageQuotaTestAccount()
	parentID := int64(7)
	account.ParentAccountID = &parentID
	require.Nil(t, newAccountLocalQuotaSnapshot(account))
}
