package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

const SettingKeyOpenAIDailyQuotaPolicy = "openai_daily_quota_policy"
const OpenAIDailyQuotaManagedKey = "openai_daily_quota_managed"

type OpenAIDailyQuotaPolicy struct {
	Enabled       bool               `json:"enabled"`
	ResetHour     int                `json:"reset_hour"`
	ResetTimezone string             `json:"reset_timezone"`
	PlanLimits    map[string]float64 `json:"plan_limits"`
	Plan5hLimits  map[string]float64 `json:"plan_5h_limits"`
}

type OpenAIDailyQuotaPlan struct {
	PlanType     string `json:"plan_type"`
	AccountCount int    `json:"account_count"`
}

type OpenAIDailyQuotaState struct {
	Policy       OpenAIDailyQuotaPolicy `json:"policy"`
	Plans        []OpenAIDailyQuotaPlan `json:"plans"`
	AccountCount int                    `json:"account_count"`
	AppliedCount int                    `json:"applied_count"`
}

// OpenAIDailyQuotaPolicyRepository keeps the setting and all affected budgets in
// one transaction. Account counters stay in the existing quota ledger.
type OpenAIDailyQuotaPolicyRepository interface {
	GetOpenAIDailyQuotaState(context.Context) (*OpenAIDailyQuotaState, error)
	SaveOpenAIDailyQuotaPolicy(context.Context, OpenAIDailyQuotaPolicy) (*OpenAIDailyQuotaState, error)
}

type OpenAIDailyQuotaPolicyAdmin interface {
	GetOpenAIDailyQuotaState(context.Context) (*OpenAIDailyQuotaState, error)
	SaveOpenAIDailyQuotaPolicy(context.Context, OpenAIDailyQuotaPolicy) (*OpenAIDailyQuotaState, error)
}

func DefaultOpenAIDailyQuotaPolicy() OpenAIDailyQuotaPolicy {
	return OpenAIDailyQuotaPolicy{ResetTimezone: "Asia/Shanghai", PlanLimits: map[string]float64{
		"plus": 30, "pro_100": 400, "pro_200": 400, "pro_500": 400,
		"free": 0, "team": 0, "business": 0, "enterprise": 0, "edu": 0, "unknown": 0,
	}, Plan5hLimits: map[string]float64{}}
}

var openAIDailyQuotaPlanPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func (p *OpenAIDailyQuotaPolicy) Validate() error {
	validLimit := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1e12 }
	if p.ResetHour < 0 || p.ResetHour > 23 {
		return errors.New("reset_hour must be between 0 and 23")
	}
	if p.ResetTimezone == "" {
		return errors.New("reset_timezone is required")
	}
	if _, err := loadQuotaResetLocation(p.ResetTimezone); err != nil {
		return errors.New("reset_timezone must be a valid IANA timezone")
	}
	if p.PlanLimits == nil {
		p.PlanLimits = map[string]float64{}
	}
	if p.Plan5hLimits == nil {
		p.Plan5hLimits = map[string]float64{}
	}
	for _, window := range []struct {
		name   string
		limits map[string]float64
	}{{"daily", p.PlanLimits}, {"5h", p.Plan5hLimits}} {
		if len(window.limits) > 100 {
			return fmt.Errorf("at most 100 %s plan overrides are allowed", window.name)
		}
		for plan, limit := range window.limits {
			if !openAIDailyQuotaPlanPattern.MatchString(plan) || plan != NormalizeOpenAIDailyQuotaPlanType(plan) || !validLimit(limit) {
				return fmt.Errorf("invalid %s limit or plan type: %s", window.name, plan)
			}
		}
	}
	return nil
}

func DecodeOpenAIDailyQuotaPolicy(raw string) (OpenAIDailyQuotaPolicy, error) {
	policy := DefaultOpenAIDailyQuotaPolicy()
	if strings.TrimSpace(raw) == "" {
		return policy, nil
	}
	// A stored policy is authoritative: absent plan keys mean unlimited, not a
	// fallback to the first-run presets.
	policy = OpenAIDailyQuotaPolicy{ResetTimezone: "Asia/Shanghai"}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return policy, err
	}
	return policy, policy.Validate()
}

func OpenAIDailyQuotaPlanType(account *Account) string {
	return NormalizeOpenAIDailyQuotaPlanType(account.GetCredential("plan_type"))
}

func NormalizeOpenAIDailyQuotaPlanType(raw string) string {
	plan := strings.ToLower(strings.TrimSpace(raw))
	if plan == "" {
		return "unknown"
	}
	switch plan {
	case "pro100", "pro-100", "pro_100":
		return "pro_100"
	case "pro200", "pro-200", "pro_200":
		return "pro_200"
	case "pro500", "pro-500", "pro_500":
		return "pro_500"
	}
	return plan
}

func (p OpenAIDailyQuotaPolicy) DailyLimit(plan string) float64 {
	return openAIQuotaPlanLimit(p.PlanLimits, plan)
}

func (p OpenAIDailyQuotaPolicy) FiveHourLimit(plan string) float64 {
	return openAIQuotaPlanLimit(p.Plan5hLimits, plan)
}

func openAIQuotaPlanLimit(limits map[string]float64, plan string) float64 {
	if limit, ok := limits[plan]; ok {
		return limit
	}
	// These are explicit OpenAI subscription SKU names already recognized by
	// the account UI (the upstream "pro" SKU is Pro 200). Retain their own stored
	// overrides, but inherit the matching tier preset when a policy saved before
	// these SKUs has no dedicated entry.
	var tier string
	switch plan {
	case "prolite":
		tier = "pro_100"
	case "pro", "chatgptpro":
		tier = "pro_200"
	case "promax":
		tier = "pro_500"
	}
	if tier != "" {
		return limits[tier]
	}
	return 0
}

func (p OpenAIDailyQuotaPolicy) WindowBounds(now time.Time) (time.Time, time.Time) {
	loc, err := loadQuotaResetLocation(p.ResetTimezone)
	if err != nil {
		loc = time.UTC
	}
	last := lastFixedDailyReset(p.ResetHour, loc, now)
	return last, nextFixedDailyReset(p.ResetHour, loc, now)
}

// ApplyToAccount changes local daily and rolling 5h budgets. Reapplying a policy
// preserves the 5h ledger; its window starts with billing, independently of the
// fixed daily reset. Only a completed day starts a fresh daily counter.
func (p OpenAIDailyQuotaPolicy) ApplyToAccount(account *Account, now time.Time) {
	if account == nil || !account.IsOpenAIOAuth() || account.IsShadow() {
		return
	}
	extra := make(map[string]any, len(account.Extra)+8)
	for key, value := range account.Extra {
		extra[key] = value
	}
	if !p.Enabled {
		if managed, _ := extra[OpenAIDailyQuotaManagedKey].(bool); !managed {
			return
		}
		for _, key := range []string{"quota_daily_limit", "quota_daily_reset_mode", "quota_daily_reset_hour", "quota_daily_reset_at", "quota_daily_reset_timezone", "quota_5h_limit", OpenAIDailyQuotaManagedKey} {
			delete(extra, key)
		}
		account.Extra = extra
		return
	}
	last, next := p.WindowBounds(now)
	extra["quota_daily_limit"] = p.DailyLimit(OpenAIDailyQuotaPlanType(account))
	extra["quota_5h_limit"] = p.FiveHourLimit(OpenAIDailyQuotaPlanType(account))
	extra["quota_daily_reset_mode"] = "fixed"
	extra["quota_daily_reset_hour"] = p.ResetHour
	extra["quota_daily_reset_timezone"] = p.ResetTimezone
	extra["quota_daily_reset_at"] = next.UTC().Format(time.RFC3339)
	extra[OpenAIDailyQuotaManagedKey] = true
	start := parseExtraTime(extra["quota_daily_start"])
	if start.IsZero() || start.Before(last) {
		extra["quota_daily_used"] = 0.0
		extra["quota_daily_start"] = last.UTC().Format(time.RFC3339)
	}
	account.Extra = extra
}

func (s *adminServiceImpl) GetOpenAIDailyQuotaState(ctx context.Context) (*OpenAIDailyQuotaState, error) {
	repo, ok := s.accountRepo.(OpenAIDailyQuotaPolicyRepository)
	if !ok {
		return nil, errors.New("daily quota policy repository unavailable")
	}
	return repo.GetOpenAIDailyQuotaState(ctx)
}

func (s *adminServiceImpl) SaveOpenAIDailyQuotaPolicy(ctx context.Context, policy OpenAIDailyQuotaPolicy) (*OpenAIDailyQuotaState, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	repo, ok := s.accountRepo.(OpenAIDailyQuotaPolicyRepository)
	if !ok {
		return nil, errors.New("daily quota policy repository unavailable")
	}
	return repo.SaveOpenAIDailyQuotaPolicy(ctx, policy)
}
