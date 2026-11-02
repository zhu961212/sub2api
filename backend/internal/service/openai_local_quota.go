package service

import (
	"context"
	"errors"
)

// CheckLocalAccountQuota refreshes scheduling and the budget for each WebSocket
// turn, including connections opened before an automatic evaluation paused the
// account. A shadow has its own scheduling switch and shares its parent's budget.
func (s *OpenAIGatewayService) CheckLocalAccountQuota(ctx context.Context, account *Account) error {
	if account == nil || !account.IsOpenAIOAuth() {
		return nil
	}
	if s == nil || s.accountRepo == nil {
		return errors.New("account quota repository unavailable")
	}
	latest, err := s.accountRepo.GetByID(ctx, account.ID)
	if err != nil {
		return err
	}
	if latest == nil || !latest.IsOpenAIOAuth() || !latest.IsSchedulable() {
		return errors.New("account is no longer schedulable")
	}
	if (latest.ParentAccountID == nil) != (account.ParentAccountID == nil) ||
		(latest.ParentAccountID != nil && *latest.ParentAccountID != *account.ParentAccountID) {
		return errors.New("account credential owner changed")
	}
	owner := latest
	if latest.ParentAccountID != nil {
		owner, err = s.accountRepo.GetByID(ctx, *latest.ParentAccountID)
		if err != nil {
			return err
		}
		// Spark scheduling is independent of the parent's manual switch and
		// global rate window. Credential failures and the shared budget still apply.
		if owner == nil || !owner.IsOpenAIOAuth() || owner.IsShadow() || !owner.IsCredentialUsableForShadow() {
			return errors.New("account credentials are unavailable")
		}
	}
	if owner.IsQuotaExceeded() {
		return errors.New("account spending quota exceeded")
	}
	return nil
}
