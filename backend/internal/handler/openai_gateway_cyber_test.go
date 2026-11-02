package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cyberQuotaUsageBillingRepo struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []service.UsageBillingCommand
}

type cyberQuotaAccountRepo struct {
	service.AccountRepository
	latest *service.Account
}

func (r *cyberQuotaAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.latest, nil
}

func (r *cyberQuotaUsageBillingRepo) Apply(_ context.Context, command *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, *command)
	return &service.UsageBillingApplyResult{Applied: false}, nil
}

func (r *cyberQuotaUsageBillingRepo) snapshot() []service.UsageBillingCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.UsageBillingCommand(nil), r.commands...)
}

type cyberQuotaBlockingModerationRepo struct {
	contentModerationHandlerTestRepo
	started chan struct{}
	release chan struct{}
}

func (r *cyberQuotaBlockingModerationRepo) CreateLog(ctx context.Context, log *service.ContentModerationLog) error {
	close(r.started)
	<-r.release
	return r.contentModerationHandlerTestRepo.CreateLog(ctx, log)
}

func TestRecordCyberPolicyIfMarked_BudgetedUsageSettlesBeforeAudit(t *testing.T) {
	for _, tc := range []struct {
		name           string
		forwardErrored bool
		wantBills      int
		fiveHourOnly   bool
		dynamicWS      bool
	}{
		{name: "failed_forward_bills_real_tokens_once", forwardErrored: true, wantBills: 1},
		{name: "failed_forward_5h_budget", forwardErrored: true, wantBills: 1, fiveHourOnly: true},
		{name: "existing_websocket_gains_5h_budget", forwardErrored: true, wantBills: 1, fiveHourOnly: true, dynamicWS: true},
		{name: "successful_forward_uses_normal_billing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := service.NewUsageRecordWorkerPoolWithOptions(service.UsageRecordWorkerPoolOptions{
				WorkerCount: 1, QueueSize: 8, TaskTimeout: time.Second, OverflowPolicy: "drop",
			})
			workerStarted, releaseWorker := make(chan struct{}), make(chan struct{})
			pool.Submit(func(context.Context) { close(workerStarted); <-releaseWorker })
			<-workerStarted
			t.Cleanup(func() { close(releaseWorker); pool.Stop() })
			moderationRepo := &cyberQuotaBlockingModerationRepo{started: make(chan struct{}), release: make(chan struct{})}
			defer close(moderationRepo.release)
			settings := &contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyRiskControlEnabled: "true"}}
			moderationService := service.NewContentModerationService(settings, moderationRepo, nil, nil, nil, nil, nil, nil)
			usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 2)}
			billingRepo := &cyberQuotaUsageBillingRepo{}
			accountRepo := &cyberQuotaAccountRepo{}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			gateway := service.NewOpenAIGatewayService(accountRepo, usageRepo, billingRepo, nil, nil, nil, nil, cfg,
				nil, nil, service.NewBillingService(cfg, nil), nil, &service.BillingCacheService{}, nil, &service.DeferredService{},
				nil, nil, nil, nil, nil, nil, nil)
			h := &OpenAIGatewayHandler{gatewayService: gateway, contentModerationService: moderationService, usageRecordWorkerPool: pool}
			c := newTestGinContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{}`))
			c.Writer.Header().Set("X-Request-Id", "cyber-quota")
			service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "blocked", UpstreamStatus: 400, UpstreamInTok: 100, UpstreamOutTok: 20})
			account := &service.Account{ID: 3, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{"quota_daily_limit": 20.0}}
			if tc.fiveHourOnly {
				account.Extra = map[string]any{"quota_5h_limit": 5.0}
			}
			accountRepo.latest = account
			if tc.dynamicWS {
				account = &service.Account{ID: account.ID, Platform: account.Platform, Type: account.Type}
				setOpsEndpointContext(c, "", int16(service.RequestTypeWSV2))
			}
			apiKey := &service.APIKey{ID: 2, User: &service.User{ID: 1}}
			h.recordCyberPolicyIfMarked(c, apiKey, account, nil, "gpt-5.1", tc.forwardErrored, nil, service.ChannelUsageFields{}, "payload")
			h.recordCyberPolicyIfMarked(c, apiKey, account, nil, "gpt-5.1", tc.forwardErrored, nil, service.ChannelUsageFields{}, "payload")
			bills := billingRepo.snapshot()
			require.Len(t, bills, tc.wantBills, "quota bills must finish without waiting for audit or queued usage, and the guard must prevent duplicates")
			if tc.wantBills > 0 {
				require.Equal(t, 100, bills[0].InputTokens)
				require.Equal(t, 20, bills[0].OutputTokens)
				require.Positive(t, bills[0].AccountQuotaCost)
				require.Zero(t, bills[0].BalanceCost)
				select {
				case log := <-usageRepo.created:
					require.Equal(t, service.RequestTypeCyberBlocked, log.RequestType)
				default:
					t.Fatal("the cyber usage row must be recorded before returning")
				}
			}
			select {
			case <-moderationRepo.started:
			case <-time.After(time.Second):
				t.Fatal("the independent audit task did not start")
			}
		})
	}
}

// newTestGinContext builds a bare gin.Context backed by an httptest recorder.
func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c
}

// TestRecordCyberPolicyIfMarked_NoMark verifies that when no cyber mark is set,
// the function returns immediately and does NOT set the recorded flag.
func TestRecordCyberPolicyIfMarked_NoMark(t *testing.T) {
	c := newTestGinContext()
	h := &OpenAIGatewayHandler{}

	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", true, nil, service.ChannelUsageFields{}, "")

	// Flag must NOT be set when there was no mark.
	require.False(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must remain false when no cyber mark is present")
}

// TestRecordCyberPolicyIfMarked_WithMark verifies that:
//  1. When a cyber mark is present, the recorded flag is set (guard activated).
//  2. A second call is a no-op (idempotent guard).
//  3. Nil services do not panic.
func TestRecordCyberPolicyIfMarked_WithMark(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
		Message:        "flagged",
		Body:           `{"error":{"code":"cyber_policy"}}`,
		UpstreamStatus: 400,
	})

	h := &OpenAIGatewayHandler{} // nil services — must not panic

	// First call: should set the flag.
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", true, nil, service.ChannelUsageFields{}, "")
	})
	require.True(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must be true after first call with a mark")

	// Second call: flag already set — must be a no-op (idempotent).
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", false, nil, service.ChannelUsageFields{}, "")
	})
	// Flag should still be true (not toggled or cleared).
	require.True(t, c.GetBool(cyberPolicyRecordedKey),
		"cyberPolicyRecordedKey must remain true after second call (guard)")
}

// TestRecordCyberPolicyIfMarked_ForwardSuccessSkipsUsageLog verifies the semantic:
// when forwardErrored=false the function still sets the guard flag (mark present),
// but the cyber usage row is NOT requested (only RecordCyberPolicyEvent fires).
// Since services are nil here we only verify the guard flag and no panic.
func TestRecordCyberPolicyIfMarked_ForwardSuccessSkipsUsageLog(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{
		Message:        "flagged",
		UpstreamStatus: 200,
	})

	h := &OpenAIGatewayHandler{}

	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", false /* forwardErrored=false */, nil, service.ChannelUsageFields{}, "")
	})
	require.True(t, c.GetBool(cyberPolicyRecordedKey))
}

// TestClearCyberPolicyTurnState verifies F1 at the handler level: after a turn
// is finalized, both the mark and the recorded guard are reset so the next WS
// turn detects/records independently.
func TestClearCyberPolicyTurnState(t *testing.T) {
	c := newTestGinContext()
	h := &OpenAIGatewayHandler{}

	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "turn1", UpstreamStatus: 200})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", false, nil, service.ChannelUsageFields{}, "")
	require.True(t, c.GetBool(cyberPolicyRecordedKey))

	clearCyberPolicyTurnState(c)
	require.Nil(t, service.GetOpsCyberPolicy(c))
	require.False(t, c.GetBool(cyberPolicyRecordedKey))

	// turn2: a fresh cyber hit must be recordable again.
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "turn2", UpstreamStatus: 200})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", false, nil, service.ChannelUsageFields{}, "")
	require.True(t, c.GetBool(cyberPolicyRecordedKey))
	require.Equal(t, "turn2", service.GetOpsCyberPolicy(c).Message)
}

func TestAdvanceOpenAIWSCyberBlockStateDefersBlockAcrossFailover(t *testing.T) {
	failoverErr := &service.UpstreamFailoverError{StatusCode: http.StatusTooManyRequests}

	blocked, pending := advanceOpenAIWSCyberBlockState(false, false, true, failoverErr)
	require.False(t, blocked, "the replacement account must receive the current turn")
	require.True(t, pending, "the cyber hit must still block later turns")

	blocked, pending = advanceOpenAIWSCyberBlockState(blocked, pending, false, failoverErr)
	require.False(t, blocked, "additional failover attempts must remain eligible")
	require.True(t, pending)

	blocked, pending = advanceOpenAIWSCyberBlockState(blocked, pending, false, nil)
	require.True(t, blocked, "the next client turn must be blocked after failover finishes")
	require.False(t, pending)
}

func TestClearCyberPolicyAttemptStatePreservesRecordedGuardDuringFailover(t *testing.T) {
	c := newTestGinContext()
	h := &OpenAIGatewayHandler{}

	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "account-a", UpstreamStatus: http.StatusOK})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", true, nil, service.ChannelUsageFields{}, "")
	require.True(t, c.GetBool(cyberPolicyRecordedKey))

	clearCyberPolicyAttemptState(c, false)
	require.Nil(t, service.GetOpsCyberPolicy(c))
	require.True(t, c.GetBool(cyberPolicyRecordedKey), "the same logical turn must not record again after failover")

	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "account-b", UpstreamStatus: http.StatusOK})
	h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", true, nil, service.ChannelUsageFields{}, "")
	require.Equal(t, "account-b", service.GetOpsCyberPolicy(c).Message)
	require.True(t, c.GetBool(cyberPolicyRecordedKey))

	clearCyberPolicyAttemptState(c, true)
	require.Nil(t, service.GetOpsCyberPolicy(c))
	require.False(t, c.GetBool(cyberPolicyRecordedKey), "a completed logical turn must reset the guard")
}

// TestBuildCyberSessionBlockedOpsEntry verifies the locally-rejected request is
// auditable: 403 / phase=request / type=cyber_policy_session_blocked — distinct
// from upstream cyber_policy hits, and it must NOT touch moderation/violation.
func TestBuildCyberSessionBlockedOpsEntry(t *testing.T) {
	entry := buildCyberSessionBlockedOpsEntry(cyberPolicyOpsErrorMeta{
		RequestID: "req-9", Model: "gpt-5", RequestPath: "/openai/v1/responses",
	})
	require.Equal(t, 403, entry.StatusCode)
	require.Equal(t, "cyber_policy_session_blocked", entry.ErrorType)
	require.Equal(t, "request", entry.ErrorPhase)
	require.True(t, entry.IsBusinessLimited)
	require.Equal(t, "gateway_local", entry.ErrorSource)
	require.Equal(t, "platform", entry.ErrorOwner)
	require.Empty(t, entry.ErrorBody, "no session block key → ErrorBody must be empty")

	entryWithKey := buildCyberSessionBlockedOpsEntry(cyberPolicyOpsErrorMeta{
		RequestID: "req-9", Model: "gpt-5", RequestPath: "/openai/v1/responses",
		SessionBlockKey: "abc123",
	})
	require.Equal(t, "session_block_key=abc123", entryWithKey.ErrorBody)
}

// TestRejectIfCyberSessionBlocked_FailOpen verifies fail-open paths: nil handler
// services, no explicit session signal, and (implicitly) disabled switch all
// pass the request through.
func TestRejectIfCyberSessionBlocked_FailOpen(t *testing.T) {
	c := newTestGinContext()
	c.Request = httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(`{}`))

	h := &OpenAIGatewayHandler{}
	require.False(t, h.rejectIfCyberSessionBlocked(c, nil, []byte(`{}`), "gpt-5", cyberBlockFormatResponses), "nil apiKey → pass")

	h2 := &OpenAIGatewayHandler{gatewayService: nil}
	key := &service.APIKey{ID: 1}
	require.False(t, h2.rejectIfCyberSessionBlocked(c, key, []byte(`{}`), "gpt-5", cyberBlockFormatResponses), "nil gateway service → pass")
}

func TestBuildCyberSessionBlockWritePlanCombinesExplicitAndTranscriptKeys(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"setup"},{"role":"assistant","content":"ready"},{"role":"user","content":"trigger"}]}`)
	c := newTestGinContext()
	c.Request = httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(string(body)))
	c.Request.RemoteAddr = "203.0.113.44:12345"
	c.Request.Header.Set("User-Agent", "client/1.2.3")

	plan := buildCyberSessionBlockWritePlan(7, c, body)
	require.Len(t, plan.keys, 2)
	require.NotEmpty(t, plan.scopeKey)

	c.Request.Header.Set("session_id", "sess-explicit")
	plan = buildCyberSessionBlockWritePlan(7, c, body)
	require.Len(t, plan.keys, 3)
	require.NotEmpty(t, plan.scopeKey)
}

// TestRecordCyberPolicyIfMarked_BlockKeyPlumbed verifies the 6th param is
// accepted and a non-empty key with nil gateway service does not panic
// (write-side guards live in the service layer).
func TestRecordCyberPolicyIfMarked_BlockKeyPlumbed(t *testing.T) {
	c := newTestGinContext()
	service.MarkOpsCyberPolicy(c, service.CyberPolicyMark{Message: "x", UpstreamStatus: 400})
	h := &OpenAIGatewayHandler{}
	require.NotPanics(t, func() {
		h.recordCyberPolicyIfMarked(c, nil, nil, nil, "gpt-5", true, []byte(`{"input":"deadbeef"}`), service.ChannelUsageFields{}, "")
	})
}

// TestBuildCyberPolicyOpsErrorEntry_StatusCode verifies F6: the ops error log
// records the status the codex client actually received (400 non-stream / 200 stream),
// not a hardcoded 403.
func TestBuildCyberPolicyOpsErrorEntry_StatusCode(t *testing.T) {
	for _, tc := range []struct {
		name           string
		upstreamStatus int
	}{
		{"non_stream_400", 400},
		{"stream_200", 200},
		{"zero_value", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mark := &service.CyberPolicyMark{
				Code:           "cyber_policy",
				Message:        "blocked",
				UpstreamStatus: tc.upstreamStatus,
			}
			entry := buildCyberPolicyOpsErrorEntry(cyberPolicyOpsErrorMeta{
				RequestID: "req-1", Model: "gpt-5", RequestPath: "/openai/v1/responses",
			}, mark)
			require.Equal(t, tc.upstreamStatus, entry.StatusCode)
			require.Equal(t, "cyber_policy", entry.ErrorType)
			require.Equal(t, "request", entry.ErrorPhase)
		})
	}
}
