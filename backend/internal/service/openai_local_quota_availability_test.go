//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type localQuotaAvailabilityRepo struct {
	AccountRepository
	accounts []Account
	owners   map[int64]*Account
	err      error
	groupID  int64
	reads    int
}

func (r *localQuotaAvailabilityRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	r.groupID = groupID
	if platform != PlatformOpenAI {
		return nil, errors.New("unexpected platform")
	}
	return r.accounts, r.err
}

func (r *localQuotaAvailabilityRepo) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	return r.accounts, r.err
}

func (r *localQuotaAvailabilityRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.reads++
	return r.owners[id], r.err
}

func localQuotaAvailabilityAccount(id int64, used float64) Account {
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"quota_daily_limit": 30.0, "quota_daily_used": used,
			"quota_daily_start": time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func TestLocalQuotaAvailabilityDistinguishesBudgetFromOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accounts []Account
		want     bool
	}{
		{"daily exhausted", []Account{localQuotaAvailabilityAccount(1, 30)}, true},
		{"healthy alternative", []Account{localQuotaAvailabilityAccount(1, 30), localQuotaAvailabilityAccount(2, 1)}, false},
		{"all exhausted", []Account{localQuotaAvailabilityAccount(1, 30), localQuotaAvailabilityAccount(2, 40)}, true},
		{"empty group", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &localQuotaAvailabilityRepo{accounts: tc.accounts}
			gateway := &OpenAIGatewayService{accountRepo: repository}
			groupID := int64(7)
			state := gateway.DiagnoseLocalAccountQuota(context.Background(), &groupID, "gpt-6.1-sol", PlatformOpenAI)
			require.Equal(t, tc.want, state.Exhausted)
			require.Equal(t, groupID, repository.groupID)
		})
	}
}

func TestLocalQuotaAvailabilityDoesNotMisclassifyManualStopsOrExpiredWindows(t *testing.T) {
	for _, reason := range []string{"manual stop", "wrong model", "expired window", "cooldown"} {
		t.Run(reason, func(t *testing.T) {
			account := localQuotaAvailabilityAccount(1, 30)
			switch reason {
			case "manual stop":
				account.Schedulable = false
			case "wrong model":
				account.Credentials["model_mapping"] = map[string]any{"other-model": "other-model"}
			case "expired window":
				account.Extra["quota_daily_start"] = time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
			case "cooldown":
				until := time.Now().Add(time.Hour)
				account.RateLimitResetAt = &until
			}
			gateway := &OpenAIGatewayService{accountRepo: &localQuotaAvailabilityRepo{accounts: []Account{account}}}
			require.False(t, gateway.DiagnoseLocalAccountQuota(context.Background(), nil, "gpt-6.1-sol", PlatformOpenAI).Exhausted)
		})
	}
}

func TestLocalQuotaAvailabilityUsesTheSharedShadowBudget(t *testing.T) {
	parent := localQuotaAvailabilityAccount(1, 30)
	first, second := localQuotaAvailabilityAccount(2, 0), localQuotaAvailabilityAccount(3, 0)
	first.ParentAccountID, second.ParentAccountID = &parent.ID, &parent.ID
	first.Extra, second.Extra = nil, nil
	repository := &localQuotaAvailabilityRepo{
		accounts: []Account{first, second}, owners: map[int64]*Account{1: &parent},
	}
	gateway := &OpenAIGatewayService{accountRepo: repository}
	require.True(t, gateway.DiagnoseLocalAccountQuota(context.Background(), nil, "gpt-6.1-sol", PlatformOpenAI).Exhausted)
	require.Equal(t, 1, repository.reads, "siblings share one fresh owner lookup")
	require.Equal(t, 30.0, parent.Extra["quota_daily_used"], "diagnosis never changes the ledger")
}

func TestLocalQuotaAvailabilityFailsWithoutInventingAnExhaustedBudget(t *testing.T) {
	gateway := &OpenAIGatewayService{accountRepo: &localQuotaAvailabilityRepo{err: errors.New("database unavailable")}}
	require.False(t, gateway.DiagnoseLocalAccountQuota(context.Background(), nil, "gpt-6.1-sol", PlatformOpenAI).Exhausted)
	require.False(t, gateway.DiagnoseLocalAccountQuota(context.Background(), nil, "gpt-6.1-sol", PlatformGrok).Exhausted)
}
