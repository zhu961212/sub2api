package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type accountLocalQuotaSnapshot struct {
	AccountID        int64     `json:"account_id"`
	UpdatedAt        time.Time `json:"updated_at"`
	QuotaLimit       float64   `json:"quota_limit"`
	QuotaUsed        float64   `json:"quota_used"`
	Quota5hLimit     float64   `json:"quota_5h_limit"`
	Quota5hUsed      float64   `json:"quota_5h_used"`
	Quota5hResetAt   string    `json:"quota_5h_reset_at"`
	QuotaDailyLimit  float64   `json:"quota_daily_limit"`
	QuotaDailyUsed   float64   `json:"quota_daily_used"`
	QuotaWeeklyLimit float64   `json:"quota_weekly_limit"`
	QuotaWeeklyUsed  float64   `json:"quota_weekly_used"`
}

type accountUsageWithLocalQuota struct {
	*service.UsageInfo
	LocalQuota *accountLocalQuotaSnapshot `json:"local_quota,omitempty"`
}

func newAccountLocalQuotaSnapshot(account *service.Account) *accountLocalQuotaSnapshot {
	if account == nil || account.IsShadow() || !account.SupportsAccountQuota() {
		return nil
	}
	snapshot := &accountLocalQuotaSnapshot{
		AccountID: account.ID, UpdatedAt: account.UpdatedAt,
		QuotaLimit: account.GetQuotaLimit(), QuotaUsed: account.GetQuotaUsed(),
		Quota5hLimit: account.GetQuota5hLimit(), Quota5hUsed: account.GetQuota5hUsed(),
		QuotaDailyLimit: account.GetQuotaDailyLimit(), QuotaDailyUsed: account.GetQuotaDailyUsed(),
		QuotaWeeklyLimit: account.GetQuotaWeeklyLimit(), QuotaWeeklyUsed: account.GetQuotaWeeklyUsed(),
	}
	if account.IsQuota5hPeriodExpired() {
		snapshot.Quota5hUsed = 0
	} else if resetAt := account.Quota5hResetAt(); !resetAt.IsZero() {
		snapshot.Quota5hResetAt = resetAt.UTC().Format(time.RFC3339)
	}
	if account.IsDailyQuotaPeriodExpired() {
		snapshot.QuotaDailyUsed = 0
	}
	if account.IsWeeklyQuotaPeriodExpired() {
		snapshot.QuotaWeeklyUsed = 0
	}
	return snapshot
}

func (h *AccountHandler) usageWithLocalQuota(ctx context.Context, accountID int64, usage *service.UsageInfo) accountUsageWithLocalQuota {
	result := accountUsageWithLocalQuota{UsageInfo: usage}
	if h.adminService == nil {
		return result
	}
	// Usage can come from an upstream cache. Read the local ledger separately
	// after sampling; never derive dollar spending from upstream percentages.
	account, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil {
		slog.WarnContext(ctx, "account usage local quota unavailable", "account_id", accountID, "error", err)
		return result
	}
	result.LocalQuota = newAccountLocalQuotaSnapshot(account)
	return result
}
