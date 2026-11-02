package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// Only edits that can alter the policy's daily configuration or plan need to
// join its lock protocol. Unrelated bulk edits retain their existing fast path.
func bulkUpdateAffectsOpenAIDailyQuota(updates service.AccountBulkUpdate) bool {
	if _, ok := updates.Credentials["plan_type"]; ok {
		return true
	}
	for _, key := range []string{
		"quota_daily_limit", "quota_5h_limit", "quota_daily_reset_mode", "quota_daily_reset_hour",
		"quota_daily_reset_at", "quota_daily_reset_timezone", service.OpenAIDailyQuotaManagedKey,
	} {
		if _, ok := updates.Extra[key]; ok {
			return true
		}
	}
	return false
}
