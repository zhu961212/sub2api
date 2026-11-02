package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIDailyQuotaPolicyPlanPresetsAndStoredZero(t *testing.T) {
	policy, err := DecodeOpenAIDailyQuotaPolicy("")
	require.NoError(t, err)
	require.Equal(t, 30.0, policy.DailyLimit("plus"))
	for _, plan := range []string{"pro", "pro_100", "pro_200", "pro_500"} {
		require.Equal(t, 400.0, policy.DailyLimit(plan))
	}
	require.Zero(t, policy.DailyLimit("free"))
	for _, raw := range []string{
		`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":15}}`,
		`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":15,"pro":0}}`,
	} {
		stored, err := DecodeOpenAIDailyQuotaPolicy(raw)
		require.NoError(t, err)
		require.Equal(t, 15.0, stored.DailyLimit("plus"))
		require.Zero(t, stored.DailyLimit("pro"))
		require.Zero(t, stored.DailyLimit("pro_100"))
		encoded, err := json.Marshal(stored)
		require.NoError(t, err)
		reloaded, err := DecodeOpenAIDailyQuotaPolicy(string(encoded))
		require.NoError(t, err)
		require.Equal(t, stored, reloaded)
	}
}

func TestOpenAIDailyQuotaPolicyFiveHourDefaultsAndIndependentPlanLimits(t *testing.T) {
	policy, err := DecodeOpenAIDailyQuotaPolicy("")
	require.NoError(t, err)
	require.NotNil(t, policy.Plan5hLimits)
	require.Zero(t, policy.FiveHourLimit("plus"))
	legacy, err := DecodeOpenAIDailyQuotaPolicy(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":20}}`)
	require.NoError(t, err)
	require.NotNil(t, legacy.Plan5hLimits)
	require.Zero(t, legacy.FiveHourLimit("plus"))
	stored, err := DecodeOpenAIDailyQuotaPolicy(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"plus":20},"plan_5h_limits":{"plus":5,"pro_100":75,"prolite":0}}`)
	require.NoError(t, err)
	require.Equal(t, 20.0, stored.DailyLimit("plus"))
	require.Equal(t, 5.0, stored.FiveHourLimit("plus"))
	require.Equal(t, 75.0, stored.FiveHourLimit("pro_100"))
	require.Zero(t, stored.FiveHourLimit("prolite"), "explicit unlimited override wins over tier fallback")
	stored.Plan5hLimits["pro_200"] = 100
	require.Equal(t, 100.0, stored.FiveHourLimit("chatgptpro"))
	require.Zero(t, stored.DailyLimit("chatgptpro"), "5h presets must not affect daily limits")
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	reloaded, err := DecodeOpenAIDailyQuotaPolicy(string(encoded))
	require.NoError(t, err)
	require.Equal(t, stored, reloaded)
}

func TestOpenAIDailyQuotaPolicyFiveHourWindowSurvivesPolicyAndDailyReset(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 30, 0, 0, time.UTC)
	started := now.Add(-time.Hour).Format(time.RFC3339)
	policy := OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "UTC", PlanLimits: map[string]float64{"plus": 20}, Plan5hLimits: map[string]float64{"plus": 5}}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}, Extra: map[string]any{
		"quota_5h_used": 3.5, "quota_5h_start": started,
		"quota_daily_used": 19.0, "quota_daily_start": now.Add(-time.Hour).Format(time.RFC3339),
	}}
	policy.ApplyToAccount(account, now)
	require.Equal(t, 5.0, account.Extra["quota_5h_limit"])
	require.Equal(t, 3.5, account.Extra["quota_5h_used"])
	require.Equal(t, started, account.Extra["quota_5h_start"])
	require.Zero(t, account.GetQuotaDailyUsed(), "daily reset must not reset the active 5h ledger")
	policy.Plan5hLimits["plus"] = 8
	policy.ResetHour = 8
	policy.ResetTimezone = "Asia/Shanghai"
	policy.ApplyToAccount(account, now.Add(time.Minute))
	require.Equal(t, 8.0, account.Extra["quota_5h_limit"])
	require.Equal(t, 3.5, account.Extra["quota_5h_used"])
	require.Equal(t, started, account.Extra["quota_5h_start"])
	newAccount := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}}
	policy.ApplyToAccount(newAccount, now)
	require.Equal(t, 8.0, newAccount.Extra["quota_5h_limit"])
	require.NotContains(t, newAccount.Extra, "quota_5h_start", "billing starts the rolling window")
	require.NotContains(t, newAccount.Extra, "quota_5h_used")
	policy.Enabled = false
	policy.ApplyToAccount(account, now)
	require.NotContains(t, account.Extra, "quota_5h_limit")
	require.Equal(t, 3.5, account.Extra["quota_5h_used"])
	require.Equal(t, started, account.Extra["quota_5h_start"])
	manual := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"quota_5h_limit": 12.0}}
	policy.ApplyToAccount(manual, now)
	require.Equal(t, 12.0, manual.Extra["quota_5h_limit"])
}

func TestOpenAIDailyQuotaPolicyPlanNormalization(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"pro", "pro"}, {" Pro100 ", "pro_100"}, {"pro-200", "pro_200"}, {"pro_500", "pro_500"},
		{"plus", "plus"}, {"", "unknown"}, {"pro_business", "pro_business"}, {"pro--100", "pro--100"},
	} {
		require.Equal(t, tc.want, NormalizeOpenAIDailyQuotaPlanType(tc.raw))
	}
}

func TestOpenAIDailyQuotaPolicyRecognizedSKUsInheritTierWithoutLosingOverrides(t *testing.T) {
	for _, tc := range []struct{ sku, tier string }{
		{"prolite", "pro_100"}, {"pro", "pro_200"}, {"chatgptpro", "pro_200"}, {"promax", "pro_500"},
	} {
		t.Run(tc.sku, func(t *testing.T) {
			policy := DefaultOpenAIDailyQuotaPolicy()
			policy.Enabled = true
			require.Equal(t, 400.0, policy.DailyLimit(tc.sku))
			policy.PlanLimits[tc.tier] = 75
			require.Equal(t, 75.0, policy.DailyLimit(tc.sku))
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": tc.sku}}
			policy.ApplyToAccount(account, time.Now())
			require.Equal(t, 75.0, account.GetQuotaDailyLimit())
			for _, override := range []float64{0, 25} {
				policy.PlanLimits[tc.sku] = override
				raw, err := json.Marshal(policy)
				require.NoError(t, err)
				stored, err := DecodeOpenAIDailyQuotaPolicy(string(raw))
				require.NoError(t, err, "existing raw-SKU policies must remain valid")
				require.Equal(t, override, stored.DailyLimit(tc.sku))
				require.Equal(t, 75.0, stored.DailyLimit(tc.tier))
				stored.ApplyToAccount(account, time.Now())
				require.Equal(t, override, account.GetQuotaDailyLimit())
			}
		})
	}
}

func TestOpenAIDailyQuotaPro200SKUUsesTierInBothWindowsAndPreservesLegacyZero(t *testing.T) {
	policy := DefaultOpenAIDailyQuotaPolicy()
	policy.Enabled = true
	policy.PlanLimits["pro_200"] = 75
	policy.Plan5hLimits["pro_200"] = 15
	for _, raw := range []string{"pro", " PRO ", "chatgptpro", "pro_200", "pro-200", "pro200"} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": raw}}
		policy.ApplyToAccount(account, time.Now())
		require.Equal(t, 75.0, account.GetQuotaDailyLimit(), raw)
		require.Equal(t, 15.0, account.Extra["quota_5h_limit"], raw)
	}
	legacy, err := DecodeOpenAIDailyQuotaPolicy(`{"enabled":true,"reset_timezone":"UTC","plan_limits":{"pro":0,"pro_200":75},"plan_5h_limits":{"pro":0,"pro_200":15}}`)
	require.NoError(t, err)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "pro"}}
	legacy.ApplyToAccount(account, time.Now())
	require.Zero(t, account.GetQuotaDailyLimit(), "an existing raw-SKU unlimited override remains authoritative")
	require.Equal(t, 0.0, account.Extra["quota_5h_limit"])
	require.Equal(t, 75.0, legacy.DailyLimit("pro_200"), "distinct stored SKU overrides are not migrated or merged")
	for _, unknown := range []string{"professional", "pro_business", "pro_300"} {
		require.Zero(t, policy.DailyLimit(unknown), "only known OpenAI SKU names inherit a tier")
		require.Zero(t, policy.FiveHourLimit(unknown))
	}
}

func TestOpenAIDailyQuotaPolicyIndependentAccountsAndPlanFallback(t *testing.T) {
	now := time.Now()
	policy := OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "Asia/Shanghai", PlanLimits: map[string]float64{"plus": 20, "free": 0}}
	for _, tc := range []struct {
		plan  string
		limit float64
	}{{" PLUS ", 20}, {"plus", 20}, {"free", 0}, {"", 0}, {"new_plan", 0}} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": tc.plan}}
		policy.ApplyToAccount(account, now)
		require.Equal(t, tc.limit, account.GetQuotaDailyLimit())
		require.Zero(t, account.GetQuotaDailyUsed())
		require.Equal(t, "fixed", account.GetQuotaDailyResetMode())
		require.Equal(t, "Asia/Shanghai", account.GetQuotaDailyResetTimezone())
	}
}

func TestOpenAIDailyQuotaPolicyWindowBoundsAcrossDaylightSaving(t *testing.T) {
	for _, tc := range []struct {
		name, now, last, next string
		hour                  int
	}{
		{"before missing hour", "2026-03-08T06:30:00Z", "2026-03-07T07:00:00Z", "2026-03-08T07:00:00Z", 2},
		{"at shifted reset", "2026-03-08T07:00:00Z", "2026-03-08T07:00:00Z", "2026-03-09T06:00:00Z", 2},
		{"after shifted reset", "2026-03-08T07:30:00Z", "2026-03-08T07:00:00Z", "2026-03-09T06:00:00Z", 2},
		{"next day restores configured hour", "2026-03-09T05:30:00Z", "2026-03-08T07:00:00Z", "2026-03-09T06:00:00Z", 2},
		{"first repeated hour", "2026-11-01T05:30:00Z", "2026-10-31T05:00:00Z", "2026-11-01T06:00:00Z", 1},
		{"at later repeated hour", "2026-11-01T06:00:00Z", "2026-11-01T06:00:00Z", "2026-11-02T06:00:00Z", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := OpenAIDailyQuotaPolicy{ResetHour: tc.hour, ResetTimezone: "America/New_York"}
			now, err := time.Parse(time.RFC3339, tc.now)
			require.NoError(t, err)
			last, next := policy.WindowBounds(now)
			require.Equal(t, tc.last, last.UTC().Format(time.RFC3339))
			require.Equal(t, tc.next, next.UTC().Format(time.RFC3339))
			require.False(t, last.After(now))
			require.True(t, next.After(now))
		})
	}
}

func TestOpenAIDailyQuotaPolicyPreservesSpendingAndWeeklyTimezone(t *testing.T) {
	now := time.Now()
	policy := OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "Asia/Shanghai", PlanLimits: map[string]float64{}}
	last, _ := policy.WindowBounds(now)
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		"quota_daily_used": 12.0, "quota_daily_start": last.Format(time.RFC3339),
		"quota_weekly_limit": 100.0, "quota_weekly_used": 34.0,
		"quota_reset_timezone": "America/New_York", "quota_weekly_reset_at": "2099-01-01T00:00:00Z",
		"quota_limit": 500.0, "quota_used": 80.0,
	}}
	policy.ApplyToAccount(account, now)
	require.Equal(t, 12.0, account.GetQuotaDailyUsed())
	require.Equal(t, 34.0, account.GetQuotaWeeklyUsed())
	require.Equal(t, 80.0, account.GetQuotaUsed())
	require.Equal(t, "America/New_York", account.GetQuotaResetTimezone())
	require.Equal(t, "2099-01-01T00:00:00Z", account.Extra["quota_weekly_reset_at"])
	policy.ApplyToAccount(account, last.AddDate(0, 0, 1).Add(time.Minute))
	require.Zero(t, account.GetQuotaDailyUsed())
}

func TestOpenAIDailyQuotaPolicyDisableOnlyManagedAccounts(t *testing.T) {
	policy := DefaultOpenAIDailyQuotaPolicy()
	manual := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 12.0}}
	policy.ApplyToAccount(manual, time.Now())
	require.Equal(t, 12.0, manual.GetQuotaDailyLimit())
	managed := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAIDailyQuotaManagedKey: true, "quota_daily_limit": 20.0, "quota_daily_used": 8.0, "quota_daily_reset_timezone": "Asia/Shanghai"}}
	policy.ApplyToAccount(managed, time.Now())
	require.Zero(t, managed.GetQuotaDailyLimit())
	require.Equal(t, 8.0, managed.GetQuotaDailyUsed())
	require.NotContains(t, managed.Extra, OpenAIDailyQuotaManagedKey)
	require.NotContains(t, managed.Extra, "quota_daily_reset_timezone")
}

func TestOpenAIDailyQuotaPolicyScopeAndValidation(t *testing.T) {
	policy := DefaultOpenAIDailyQuotaPolicy()
	policy.Enabled = true
	parentID := int64(1)
	for _, account := range []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID},
	} {
		policy.ApplyToAccount(account, time.Now())
		require.Nil(t, account.Extra)
	}
	for _, mutate := range []func(*OpenAIDailyQuotaPolicy){
		func(p *OpenAIDailyQuotaPolicy) { p.PlanLimits["plus"] = -1 },
		func(p *OpenAIDailyQuotaPolicy) { p.PlanLimits["plus"] = math.NaN() },
		func(p *OpenAIDailyQuotaPolicy) { p.ResetHour = 24 },
		func(p *OpenAIDailyQuotaPolicy) { p.ResetTimezone = "bad/timezone" },
		func(p *OpenAIDailyQuotaPolicy) { p.PlanLimits["plus"] = math.Inf(1) },
		func(p *OpenAIDailyQuotaPolicy) { p.PlanLimits["Plus"] = 1 },
		func(p *OpenAIDailyQuotaPolicy) { p.Plan5hLimits["plus"] = -1 },
		func(p *OpenAIDailyQuotaPolicy) { p.Plan5hLimits["plus"] = math.NaN() },
		func(p *OpenAIDailyQuotaPolicy) { p.Plan5hLimits["plus"] = math.Inf(1) },
		func(p *OpenAIDailyQuotaPolicy) { p.Plan5hLimits["plus"] = 1e12 + 1 },
		func(p *OpenAIDailyQuotaPolicy) { p.Plan5hLimits["Plus"] = 1 },
	} {
		invalid := DefaultOpenAIDailyQuotaPolicy()
		mutate(&invalid)
		require.Error(t, invalid.Validate())
	}
}

func TestOpenAIOAuthLocalQuotaSchedulingAndDailyRecovery(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	policy := OpenAIDailyQuotaPolicy{Enabled: true, ResetTimezone: "Asia/Shanghai", PlanLimits: map[string]float64{"unknown": 10}}
	policy.ApplyToAccount(account, time.Now())
	require.True(t, account.SupportsAccountQuota())
	require.False(t, account.IsAPIKeyOrBedrock())
	account.Extra["quota_daily_used"] = 10.0
	require.False(t, account.IsSchedulable())
	shadow := &Account{ParentAccountID: &account.ID}
	require.False(t, parentHealthyForShadow(shadow, func(int64) *Account { return account }))
	account.Extra["quota_daily_start"] = time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	require.True(t, account.IsSchedulable())
	require.True(t, parentHealthyForShadow(shadow, func(int64) *Account { return account }))
	account.Platform = PlatformAnthropic
	require.False(t, account.SupportsAccountQuota())
}

func TestOpenAIOAuthQuotaBillingAndShadowAttribution(t *testing.T) {
	parent := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 20.0}}
	shadow := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID}
	p := &postUsageBillingParams{Cost: &CostBreakdown{TotalCost: 2, ActualCost: 9}, User: &User{ID: 1}, APIKey: &APIKey{ID: 2}, Account: shadow, QuotaAccount: parent, AccountRateMultiplier: 1.5}
	cmd := buildUsageBillingCommand("quota-shadow", nil, p)
	require.Equal(t, shadow.ID, cmd.AccountID)
	require.Equal(t, parent.ID, cmd.AccountQuotaAccountID)
	require.Equal(t, PlatformOpenAI, cmd.AccountPlatform)
	require.Equal(t, 3.0, cmd.AccountQuotaCost)
	require.True(t, cmd.AccountQuotaHasDailyLimit)
	p.SimpleModeAccountQuota = true
	cmd = buildUsageBillingCommand("quota-simple", nil, p)
	require.Equal(t, 3.0, cmd.AccountQuotaCost)
	require.Zero(t, cmd.BalanceCost)
	require.Zero(t, cmd.SubscriptionCost)
	require.Zero(t, cmd.APIKeyQuotaCost)
}

type openAILocalQuotaRepoStub struct {
	AccountRepository
	account  *Account
	accounts map[int64]*Account
	id       int64
}

func (r *openAILocalQuotaRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	r.id = id
	if r.accounts != nil {
		return r.accounts[id], nil
	}
	return r.account, nil
}

func TestOpenAIWebSocketQuotaReadsCurrentParent(t *testing.T) {
	parentID := int64(7)
	repo := &openAILocalQuotaRepoStub{account: &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{"quota_daily_limit": 5.0, "quota_daily_used": 5.0, "quota_daily_start": time.Now().Format(time.RFC3339)}}}
	svc := &OpenAIGatewayService{accountRepo: repo}
	selected := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ParentAccountID: &parentID}
	repo.accounts = map[int64]*Account{parentID: repo.account, selected.ID: selected}
	require.Error(t, svc.CheckLocalAccountQuota(context.Background(), selected))
	require.Equal(t, parentID, repo.id)
	repo.account.Extra["quota_daily_start"] = time.Now().Add(-25 * time.Hour).Format(time.RFC3339)
	require.NoError(t, svc.CheckLocalAccountQuota(context.Background(), selected))
}
