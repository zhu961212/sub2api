package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildUsageBillingCommand_AccountQuotaUsesAccountPricing(t *testing.T) {
	t.Parallel()

	accountPrice := 2.0
	zeroPrice := 0.0
	for _, tt := range []struct {
		name             string
		totalCost        float64
		actualCost       float64
		accountStatsCost *float64
		wantQuotaCost    float64
	}{
		{name: "account_price_overrides_customer_price", totalCost: 5, actualCost: 10, accountStatsCost: &accountPrice, wantQuotaCost: 3},
		{name: "free_customer_still_consumes_account_budget", accountStatsCost: &accountPrice, wantQuotaCost: 3},
		{name: "zero_user_multiplier", totalCost: 5, accountStatsCost: &accountPrice, wantQuotaCost: 3},
		{name: "explicit_zero_account_price", totalCost: 5, actualCost: 10, accountStatsCost: &zeroPrice},
		{name: "missing_account_price_falls_back_to_raw_cost", totalCost: 5, actualCost: 10, wantQuotaCost: 7.5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, simpleMode := range []bool{false, true} {
				p := &postUsageBillingParams{
					Cost:                   &CostBreakdown{TotalCost: tt.totalCost, ActualCost: tt.actualCost},
					User:                   &User{ID: 1},
					APIKey:                 &APIKey{ID: 2},
					Account:                &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 20.0}},
					AccountRateMultiplier:  1.5,
					AccountStatsCost:       tt.accountStatsCost,
					SimpleModeAccountQuota: simpleMode,
				}
				cmd := buildUsageBillingCommand("account-pricing", nil, p)
				require.Equal(t, tt.wantQuotaCost, cmd.AccountQuotaCost)
				if !simpleMode {
					require.Equal(t, tt.actualCost, cmd.BalanceCost)
				} else {
					require.Zero(t, cmd.BalanceCost)
				}
			}
		})
	}
}

func TestPostUsageBilling_AccountQuotaUsesAccountPricing(t *testing.T) {
	accountPrice := 2.0
	accountRepo := &accountQuotaIncrementRepoStub{}
	userRepo := &openAIRecordUsageUserRepoStub{}
	postUsageBilling(context.Background(), &postUsageBillingParams{
		Cost:                  &CostBreakdown{TotalCost: 5, ActualCost: 10},
		User:                  &User{ID: 1},
		APIKey:                &APIKey{ID: 2},
		Account:               &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 20.0}},
		AccountRateMultiplier: 1.5,
		AccountStatsCost:      &accountPrice,
	}, &billingDeps{accountRepo: accountRepo, userRepo: userRepo})
	require.Equal(t, []float64{3}, accountRepo.calls)
	require.Equal(t, 10.0, userRepo.lastAmount)
}

type accountQuotaCacheStub struct {
	SchedulerCache
	account *Account
	calls   int
}

func (c *accountQuotaCacheStub) SetAccount(_ context.Context, account *Account) error {
	c.account = account
	c.calls++
	return nil
}

func TestApplyUsageBilling_RefreshesExceededQuotaOwnerBeforeReturning(t *testing.T) {
	for _, tt := range []struct {
		name     string
		used     float64
		applied  bool
		want     int
		fiveHour bool
	}{
		{name: "below_limit", used: 4, applied: true},
		{name: "crossed_limit", used: 6, applied: true, want: 1},
		{name: "already_over_limit", used: 8, applied: true},
		{name: "duplicate_not_applied", used: 6},
		{name: "5h_below_limit", used: 4, applied: true, fiveHour: true},
		{name: "5h_crossed_limit", used: 6, applied: true, want: 1, fiveHour: true},
		{name: "5h_already_over_limit", used: 8, applied: true, fiveHour: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Extra: map[string]any{"quota_daily_limit": 5.0, "quota_daily_used": tt.used}}
			state := &AccountQuotaState{DailyUsed: tt.used, DailyLimit: 5}
			if tt.fiveHour {
				parent.Extra = map[string]any{"quota_5h_limit": 5.0, "quota_5h_used": tt.used}
				state = &AccountQuotaState{FiveHourUsed: tt.used, FiveHourLimit: 5}
			}
			shadow := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID}
			accountRepo := &openAIRecordUsageAccountRepoStub{account: parent}
			cache := &accountQuotaCacheStub{}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{
				Applied: tt.applied, QuotaState: state,
			}}
			_, err := applyUsageBilling(context.Background(), "quota-cache", nil, &postUsageBillingParams{
				Cost: &CostBreakdown{TotalCost: 2}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2},
				Account: shadow, QuotaAccount: parent, AccountRateMultiplier: 1, SimpleModeAccountQuota: true,
			}, &billingDeps{
				accountRepo: accountRepo, deferredService: &DeferredService{},
				schedulerSnapshot: &SchedulerSnapshotService{cache: cache},
			}, billingRepo)
			require.NoError(t, err)
			require.Equal(t, tt.want, cache.calls)
			require.Equal(t, tt.want, accountRepo.calls)
			if tt.want > 0 {
				require.Same(t, parent, cache.account, "the credential owner's quota cache must refresh, including shadow bills")
			}
		})
	}
}

func TestBuildUsageBillingCommand_AccountQuotaPeriodHints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		accountType string
		extra       map[string]any
		wantQuota   bool
		wantDaily   bool
		wantWeekly  bool
	}{
		{name: "no_limits", extra: map[string]any{}},
		{name: "total_only", extra: map[string]any{"quota_limit": 10.0}, wantQuota: true},
		{name: "daily_only", extra: map[string]any{"quota_daily_limit": 5.0}, wantQuota: true, wantDaily: true},
		{name: "5h_only", extra: map[string]any{"quota_5h_limit": 5.0}, wantQuota: true},
		{name: "weekly_only", extra: map[string]any{"quota_weekly_limit": 5.0}, wantQuota: true, wantWeekly: true},
		{
			name:      "all_dimensions",
			extra:     map[string]any{"quota_limit": 10.0, "quota_daily_limit": 5.0, "quota_weekly_limit": 7.0},
			wantQuota: true, wantDaily: true, wantWeekly: true,
		},
		{
			name:      "non_positive_period_limits",
			extra:     map[string]any{"quota_limit": 10.0, "quota_daily_limit": 0.0, "quota_weekly_limit": -1.0},
			wantQuota: true,
		},
		{
			name:        "service_account_total_only",
			accountType: AccountTypeServiceAccount,
			extra:       map[string]any{"quota_limit": 10.0},
			wantQuota:   true,
		},
		{
			name:        "service_account_daily",
			accountType: AccountTypeServiceAccount,
			extra:       map[string]any{"quota_limit": 10.0, "quota_daily_limit": 5.0},
			wantQuota:   true, wantDaily: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			accountType := tt.accountType
			if accountType == "" {
				accountType = AccountTypeAPIKey
			}
			p := &postUsageBillingParams{
				Cost:                  &CostBreakdown{TotalCost: 1, ActualCost: 1},
				User:                  &User{ID: 1},
				APIKey:                &APIKey{ID: 2},
				Account:               &Account{ID: 3, Type: accountType, Extra: tt.extra},
				AccountRateMultiplier: 1,
			}

			cmd := buildUsageBillingCommand("req-1", nil, p)
			require.NotNil(t, cmd)
			require.Equal(t, tt.wantQuota, cmd.AccountQuotaCost > 0)
			require.Equal(t, tt.wantDaily, cmd.AccountQuotaHasDailyLimit)
			require.Equal(t, tt.wantWeekly, cmd.AccountQuotaHasWeeklyLimit)
		})
	}
}

func TestUsageBillingCommandFingerprintIgnoresAccountQuotaPeriodHints(t *testing.T) {
	t.Parallel()

	base := UsageBillingCommand{
		RequestID:        "req-1",
		UserID:           1,
		APIKeyID:         2,
		AccountID:        3,
		AccountType:      AccountTypeAPIKey,
		BalanceCost:      1,
		AccountQuotaCost: 1,
	}
	hinted := base
	hinted.AccountQuotaHasDailyLimit = true
	hinted.AccountQuotaHasWeeklyLimit = true

	base.Normalize()
	hinted.Normalize()
	require.NotEmpty(t, base.RequestFingerprint)
	require.Equal(t, base.RequestFingerprint, hinted.RequestFingerprint)
}
