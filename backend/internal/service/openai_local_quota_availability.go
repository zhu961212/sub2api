package service

import (
	"context"
	"maps"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type LocalAccountQuotaState struct {
	Exhausted bool
}

func accountWithoutLocalBudget(account *Account) *Account {
	copy := *account
	copy.Extra = maps.Clone(account.Extra)
	for _, key := range []string{"quota_limit", "quota_5h_limit", "quota_daily_limit", "quota_weekly_limit"} {
		delete(copy.Extra, key)
	}
	return &copy
}

// DiagnoseLocalAccountQuota runs only after selection fails. A fresh database
// read distinguishes a local spending gate from missing models or a busy pool,
// without adding a query to successful requests or changing official selection.
func (s *OpenAIGatewayService) DiagnoseLocalAccountQuota(ctx context.Context, groupID *int64, model, platform string) LocalAccountQuotaState {
	if s == nil || s.accountRepo == nil || NormalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return LocalAccountQuotaState{}
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var accounts []Account
	var err error
	switch {
	case s.cfg != nil && s.cfg.RunMode == config.RunModeSimple:
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, PlatformOpenAI)
	case groupID != nil:
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, PlatformOpenAI)
	default:
		accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, PlatformOpenAI)
	}
	if err != nil {
		return LocalAccountQuotaState{}
	}
	var group *Group
	if groupID != nil && s.schedulerSnapshot != nil {
		group, err = s.schedulerSnapshot.GetGroupByIDLite(ctx, *groupID)
		if err != nil {
			return LocalAccountQuotaState{}
		}
	}
	owners := make(map[int64]*Account)
	exhausted := false
	for i := range accounts {
		account := &accounts[i]
		if group != nil && group.RequirePrivacySet && !account.IsPrivacySet() {
			continue
		}
		unlimited := accountWithoutLocalBudget(account)
		if !isOpenAICompatibleAccountEligibleForRequestBeforeProfit(ctx, unlimited, PlatformOpenAI, model, false, "") ||
			s.isOpenAIAccountRequestRuntimeBlocked(account, model) ||
			s.isOpenAIProxyStreamQuarantined(ctx, account) {
			continue
		}
		if vetoed, _ := openAIProfitControlVetoReason(ctx, account); vetoed {
			continue
		}
		if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
			s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, model, false) {
			continue
		}
		owner := account
		if account.ParentAccountID != nil {
			id := *account.ParentAccountID
			var found bool
			owner, found = owners[id]
			if !found {
				owner, err = s.accountRepo.GetByID(ctx, id)
				if err != nil {
					return LocalAccountQuotaState{}
				}
				owners[id] = owner
			}
			if owner == nil || !owner.IsOpenAIOAuth() || !owner.IsCredentialUsableForShadow() {
				continue
			}
		}
		if owner.IsOpenAIOAuth() && owner.IsQuotaExceeded() {
			exhausted = true
			continue
		}
		// At least one model-capable account is not budget-blocked. Its selection
		// failed for another reason, so retain the official failure classification.
		return LocalAccountQuotaState{}
	}
	return LocalAccountQuotaState{Exhausted: exhausted}
}
