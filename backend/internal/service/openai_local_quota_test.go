package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWebSocketTurnsRespectCurrentSchedulingAndShadowIsolation(t *testing.T) {
	for _, test := range []struct {
		name    string
		shadow  bool
		change  func(selected, parent *Account, accounts map[int64]*Account)
		blocked bool
	}{
		{"parent automatically paused", false, func(a, _ *Account, _ map[int64]*Account) { a.Schedulable = false }, true},
		{"parent disabled", false, func(a, _ *Account, _ map[int64]*Account) { a.Status = StatusDisabled }, true},
		{"selected shadow automatically paused", true, func(a, parent *Account, _ map[int64]*Account) { a.Schedulable, parent.Schedulable = false, false }, true},
		{"selected shadow deleted", true, func(a, _ *Account, accounts map[int64]*Account) { delete(accounts, a.ID) }, true},
		{"parent global pause does not pause spark", true, func(_, parent *Account, _ map[int64]*Account) {
			future := time.Now().Add(time.Hour)
			parent.Schedulable, parent.RateLimitResetAt = false, &future
		}, false},
		{"selected shadow rate limited", true, func(a, _ *Account, _ map[int64]*Account) {
			future := time.Now().Add(time.Hour)
			a.RateLimitResetAt = &future
		}, true},
		{"parent credentials temporarily paused", true, func(_, parent *Account, _ map[int64]*Account) {
			future := time.Now().Add(time.Hour)
			parent.TempUnschedulableUntil = &future
		}, true},
		{"shadow credential owner changed", true, func(a, _ *Account, _ map[int64]*Account) { different := int64(9); a.ParentAccountID = &different }, true},
		{"shadow credential owner removed", true, func(a, _ *Account, _ map[int64]*Account) { a.ParentAccountID = nil }, true},
		{"parent changed into a shadow", false, func(a, _ *Account, _ map[int64]*Account) { owner := int64(9); a.ParentAccountID = &owner }, true},
		{"active parent", false, func(_, _ *Account, _ map[int64]*Account) {}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
			selected := parent
			accounts := map[int64]*Account{parent.ID: parent}
			if test.shadow {
				selected = &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, ParentAccountID: &parent.ID}
				accounts[selected.ID] = selected
			}
			connectionSnapshot := *selected
			test.change(selected, parent, accounts)
			gateway := &OpenAIGatewayService{accountRepo: &openAILocalQuotaRepoStub{accounts: accounts}}
			err := gateway.CheckLocalAccountQuota(context.Background(), &connectionSnapshot)
			if test.blocked {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
