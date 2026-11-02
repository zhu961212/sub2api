//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func evaluationCompletedStream(answer string) string {
	text, _ := json.Marshal(answer)
	return fmt.Sprintf(`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%s}]}],"usage":{"input_tokens":30,"output_tokens":20,"output_tokens_details":{"reasoning_tokens":10}}}}`, text) + "\n\n"
}

func TestModelEvaluationRequestValidation(t *testing.T) {
	got, err := normalizeModelEvaluationRequest(ModelEvaluationRequest{})
	require.NoError(t, err)
	require.Equal(t, "candy", got.Kind)
	require.Equal(t, openai.DefaultTestModel, got.ModelID)
	require.Equal(t, "low", got.Effort)
	require.Equal(t, 1, got.Runs)
	for _, req := range []ModelEvaluationRequest{
		{Kind: "fingerprint"}, {Runs: -1}, {Runs: 11}, {Effort: "extreme"}, {ModelID: "model\nheader"},
	} {
		_, err := normalizeModelEvaluationRequest(req)
		require.Error(t, err)
	}
}

func TestModelEvaluationLeaseDurationCapsFutureBudgets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runs    int
		timeout time.Duration
		want    time.Duration
	}{
		{"single run", 1, 10 * time.Minute, 12 * time.Minute},
		{"current maximum runs", 10, 10 * time.Minute, 102 * time.Minute},
		{"exact repository boundary", 59, 2 * time.Minute, ModelEvaluationMaxLeaseDuration},
		{"future longer timeout", 10, 15 * time.Minute, ModelEvaluationMaxLeaseDuration},
		{"future larger run count", 20, 10 * time.Minute, ModelEvaluationMaxLeaseDuration},
		{"multiplication cannot overflow", 10, time.Duration(1<<63 - 1), ModelEvaluationMaxLeaseDuration},
		{"invalid runs", 0, time.Minute, 0},
		{"invalid timeout", 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, modelEvaluationLeaseDuration(tc.runs, tc.timeout)) })
	}
}

func TestModelEvaluationStreamReportsFrameAndAnswerLimitsSeparately(t *testing.T) {
	result := ModelEvaluationResult{}
	err := readModelEvaluationStream(strings.NewReader("data: "+strings.Repeat("x", 1<<20)+"\n\n"), &result, nil)
	require.EqualError(t, err, "upstream response event line exceeded 1 MB")
	err = readModelEvaluationStream(strings.NewReader(evaluationCompletedStream(strings.Repeat("a", modelEvaluationMaxAnswer+1))), &result, nil)
	require.EqualError(t, err, "model answer exceeded 256 KB")
}

func TestModelEvaluationStreamRequiresCompletedText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream string
		want   string
	}{
		{"final answer", evaluationCompletedStream("答案是21"), "答案是21"},
		{"unterminated final line", strings.TrimSpace(evaluationCompletedStream("21")), "21"},
		{"partial correct then EOF", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\n", ""},
		{"partial then failure", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"21\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"secret-token\"}}}\n\n", ""},
		{"incomplete", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n\n", ""},
		{"tool only", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\"}]}}\n\n", ""},
		{"done without response", "data: [DONE]\n\n", ""},
		{"invalid JSON", "data: invalid\n\n", ""},
		{"empty answer", evaluationCompletedStream("  "), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ModelEvaluationResult{}
			err := readModelEvaluationStream(strings.NewReader(tc.stream), &result, nil)
			if tc.want == "" {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret-token")
				require.Empty(t, result.Answer)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, result.Answer)
				require.EqualValues(t, 30, result.InputTokens)
				require.EqualValues(t, 20, result.OutputTokens)
				require.EqualValues(t, 10, result.ReasoningTokens)
			}
		})
	}
}

func TestModelEvaluationCandyGradingMatchesReference(t *testing.T) {
	for answer, expected := range map[string]string{
		"21": "correct", "最少取出**21**个": "correct", "答案：21。": "correct",
		"121": "wrong", "210": "wrong", "2 1": "wrong", "28": "wrong",
	} {
		result := ModelEvaluationResult{Kind: "candy", Answer: answer}
		require.NoError(t, gradeModelEvaluation(&result))
		require.Equal(t, expected, result.Status, answer)
	}
}

func TestModelEvaluationPelicanSVG(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		valid  bool
	}{
		{"animated", "```svg\n<svg xmlns=\"http://www.w3.org/2000/svg\"><circle r=\"2\"><animate attributeName=\"r\" values=\"2;3;2\" dur=\"1s\" repeatCount=\"indefinite\"/></circle></svg>\n```", true},
		{"local gradient", `<svg><defs><linearGradient id="a"/></defs><rect fill="url(#a)"/></svg>`, true},
		{"html wrapping", `<html><body><svg><text>pelican</text></svg></body></html>`, true},
		{"not markup", "21", false},
		{"broken markup", `<svg><circle></svg>`, false},
		{"script", `<svg><script>alert(1)</script></svg>`, false},
		{"event handler", `<svg onload="alert(1)"></svg>`, false},
		{"foreign object", `<svg><foreignObject><p>test</p></foreignObject></svg>`, false},
		{"remote image", `<svg><image href="https://example.com/a.png"/></svg>`, false},
		{"relative asset", `<svg><rect style="fill:url(asset.svg)"/></svg>`, false},
		{"remote paint", `<svg><rect fill="url('https://example.com/a.svg')"/></svg>`, false},
		{"animated href", `<svg><set attributeName="href" to="https://example.com"/></svg>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ModelEvaluationResult{Kind: "pelican", Answer: tc.answer}
			err := gradeModelEvaluation(&result)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, "completed", result.Status)
				require.True(t, strings.HasPrefix(result.SVG, "<svg"))
			} else {
				require.Error(t, err)
				require.Empty(t, result.SVG)
			}
		})
	}
}

type modelEvaluationRepo struct {
	openAIAccountTestRepo
	writes []map[string]any
}

func (r *modelEvaluationRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.writes = append(r.writes, updates)
	account := r.accountsByID[id]
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	for key, value := range updates {
		account.Extra[key] = value
	}
	if history, ok := updates[ModelEvaluationResultsExtraKey].([]ModelEvaluationResult); ok && len(history) > 0 {
		latest := history[0]
		if latest.Kind == "candy" {
			account.Schedulable = latest.Status == "correct"
			latest.SchedulingStopped = latest.Status == "wrong"
			latest.SchedulingResumed = latest.Status == "correct"
			history[0] = latest
			account.Extra[ModelEvaluationResultsExtraKey] = history
		}
	}
	return nil
}

func TestModelEvaluationRunsPersistResultsAndApplyCandyScheduling(t *testing.T) {
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-secret"},
		Extra:       map[string]any{"quota_daily_used": 5.0},
	}
	repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{
		newJSONResponse(http.StatusOK, evaluationCompletedStream("21")),
		newJSONResponse(http.StatusOK, evaluationCompletedStream("28")),
	}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	ctx, recorder := newTestContext()
	require.NoError(t, svc.EvaluateOpenAIAccount(ctx, 7, ModelEvaluationRequest{Kind: "candy", ModelID: "gpt-5.4", Effort: "high", Runs: 2}))
	require.Len(t, upstream.requests, 2)
	require.Len(t, repo.writes, 2)
	require.Len(t, repo.writes[0], 1)
	require.False(t, account.Schedulable, "the latest wrong candy answer stops scheduling")
	require.Equal(t, 5.0, account.Extra["quota_daily_used"])
	history := account.Extra[ModelEvaluationResultsExtraKey].([]ModelEvaluationResult)
	require.Len(t, history, 2)
	require.Equal(t, "wrong", history[0].Status)
	require.Equal(t, "correct", history[1].Status)
	require.Zero(t, repo.setErrorID)
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Contains(t, recorder.Body.String(), `"type":"evaluation_result"`)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete","success":true`)
	require.NotContains(t, recorder.Body.String(), "test-secret")
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, modelEvaluationCandyPrompt, gjson.GetBytes(body, "input.0.content.0.text").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.Equal(t, "none", gjson.GetBytes(body, "tool_choice").String())
	require.False(t, gjson.GetBytes(body, "store").Bool())
}

func TestModelEvaluationPreservesRequestedReasoningEffort(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh"} {
		t.Run(effort, func(t *testing.T) {
			account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "test-token"},
			}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, evaluationCompletedStream("21"))}}
			svc := &AccountTestService{httpUpstream: upstream}
			result, err := svc.runModelEvaluation(context.Background(), account, ModelEvaluationRequest{Effort: effort}, "automatic", 1, nil)
			require.NoError(t, err)
			require.Equal(t, "correct", result.Status)
			require.Equal(t, effort, result.Effort)
			require.Len(t, upstream.requests, 1)
			body, err := io.ReadAll(upstream.requests[0].Body)
			require.NoError(t, err)
			require.Equal(t, effort, gjson.GetBytes(body, "reasoning.effort").String(), "none must be explicit so upstream defaults cannot override the recorded effort")
		})
	}
}

func TestModelEvaluationUpstreamRejectionIsFailedAndSafe(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			account := &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "test-secret"}}
			resetAt := time.Now().Add(time.Hour)
			if status == http.StatusTooManyRequests {
				account.RateLimitResetAt = &resetAt
			}
			repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{8: account}}}}
			upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(status, `{"error":"test-secret 21"}`)}}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			ctx, recorder := newTestContext()
			require.NoError(t, svc.EvaluateOpenAIAccount(ctx, 8, ModelEvaluationRequest{}))
			history := account.Extra[ModelEvaluationResultsExtraKey].([]ModelEvaluationResult)
			require.Equal(t, "failed", history[0].Status)
			require.Equal(t, "", history[0].Answer)
			require.Contains(t, history[0].Error, fmt.Sprint(status))
			require.Zero(t, repo.setErrorID)
			require.Zero(t, repo.rateLimitedID)
			if status == http.StatusTooManyRequests {
				require.Equal(t, resetAt, *account.RateLimitResetAt, "manual diagnosis must preserve the existing cooldown")
				require.Contains(t, history[0].Error, "rate limited")
				require.True(t, account.Schedulable)
			}
			require.NotContains(t, recorder.Body.String(), "test-secret")
			require.Contains(t, recorder.Body.String(), `"answer":""`)
		})
	}
}

func TestModelEvaluationRejectsUnsupportedAccountsAndConcurrentRuns(t *testing.T) {
	account := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{9: account}}}}
	upstream := &queuedHTTPUpstream{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	ctx, recorder := newTestContext()
	require.Error(t, svc.EvaluateOpenAIAccount(ctx, 9, ModelEvaluationRequest{}))
	require.False(t, ctx.Writer.Written())
	account.Type = AccountTypeOAuth
	svc.modelEvaluationRuns.Store(int64(9), struct{}{})
	require.Error(t, svc.EvaluateOpenAIAccount(ctx, 9, ModelEvaluationRequest{}))
	require.Empty(t, recorder.Body.String())
	require.Empty(t, upstream.requests)
	require.Empty(t, repo.writes)
}

func TestModelEvaluationCancellationDoesNotGradeOrPersist(t *testing.T) {
	account := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{10: account}}}}
	upstream := &queuedHTTPUpstream{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	ctx, recorder := newTestContext()
	cancelled, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(cancelled)
	require.ErrorIs(t, svc.EvaluateOpenAIAccount(ctx, 10, ModelEvaluationRequest{}), context.Canceled)
	require.NotContains(t, recorder.Body.String(), "evaluation_result")
	require.Empty(t, upstream.requests)
	require.Empty(t, repo.writes)
	_, running := svc.modelEvaluationRuns.Load(int64(10))
	require.False(t, running)
}

func TestModelEvaluationShadowDefaultUsesSparkAndParentCredentials(t *testing.T) {
	parentID := int64(100)
	parent := &Account{ID: parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "parent-secret"},
	}
	shadow := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark,
		Credentials: map[string]any{"model_mapping": defaultSparkShadowModelMapping()},
	}
	repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{parentID: parent, 200: shadow}}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK, evaluationCompletedStream("21"))}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	ctx, recorder := newTestContext()
	require.NoError(t, svc.EvaluateOpenAIAccount(ctx, shadow.ID, ModelEvaluationRequest{}))
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer parent-secret", upstream.requests[0].Header.Get("Authorization"))
	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.3-codex-spark", gjson.GetBytes(body, "model").String())
	require.Contains(t, recorder.Body.String(), `"model":"gpt-5.3-codex-spark"`)
	require.NotContains(t, recorder.Body.String(), "parent-secret")
	require.Nil(t, parent.Extra)

	// Explicit unsupported selections must not silently become another model.
	ctx, _ = newTestContext()
	require.Error(t, svc.EvaluateOpenAIAccount(ctx, shadow.ID, ModelEvaluationRequest{ModelID: "gpt-5.4"}))
	require.Len(t, upstream.requests, 1)
}

func TestModelEvaluationDefaultRespectsTextModelWhitelist(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{"draw": "gpt-image-2", "custom-text": "gpt-5.4"},
	}}
	require.Equal(t, "custom-text", defaultModelEvaluationModel(account))
}

func TestModelEvaluationHeartbeatStopJoinsActiveWriter(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelFirst), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var writes atomic.Int64
			stop := startModelEvaluationHeartbeat(ctx, time.Millisecond, func() {
				if writes.Add(1) == 1 {
					close(entered)
					<-release
				}
			})
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("heartbeat did not start")
			}
			if cancelFirst {
				cancel()
			}
			go func() { stop(); close(stopped) }()
			select {
			case <-stopped:
				t.Fatal("stop returned while the response writer was still active")
			case <-time.After(5 * time.Millisecond):
			}
			close(release)
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("heartbeat leaked after stopping")
			}
			stop() // Idempotent; it is safe after cancellation and normal completion.
			before := writes.Load()
			time.Sleep(5 * time.Millisecond)
			require.Equal(t, before, writes.Load(), "writer was accessed after stop returned")
		})
	}
}

func TestModelEvaluationHistoryIsServerManagedInAdminWrites(t *testing.T) {
	accountID := int64(300)
	history := []ModelEvaluationResult{{ID: "latest", Kind: "pelican", Answer: "full answer", SVG: "<svg/>"}}
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		accountID: {ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
			Credentials: map[string]any{"access_token": "token"}, Extra: map[string]any{ModelEvaluationResultsExtraKey: history, ModelEvaluationPauseExtraKey: map[string]any{"reason": "candy_wrong"}}},
	}}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{Extra: map[string]any{
		ModelEvaluationResultsExtraKey: []any{map[string]any{"id": "latest", "answer": ""}}, ModelEvaluationPauseExtraKey: nil, "custom": "edited",
	}})
	require.NoError(t, err)
	require.Equal(t, history, updated.Extra[ModelEvaluationResultsExtraKey])
	require.Equal(t, map[string]any{"reason": "candy_wrong"}, updated.Extra[ModelEvaluationPauseExtraKey])
	require.Equal(t, "edited", updated.Extra["custom"])

	require.NoError(t, svc.UpdateAccountExtra(context.Background(), accountID, map[string]any{
		ModelEvaluationResultsExtraKey: []any{}, ModelEvaluationPauseExtraKey: nil, "custom": "patch",
	}))
	require.Len(t, repo.updates[accountID], 1)
	require.NotContains(t, repo.updates[accountID][0], ModelEvaluationResultsExtraKey)
	require.NotContains(t, repo.updates[accountID][0], ModelEvaluationPauseExtraKey)

	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{accountID}, Extra: map[string]any{ModelEvaluationResultsExtraKey: []any{}, ModelEvaluationPauseExtraKey: nil, "custom": "bulk"},
	})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.NotContains(t, repo.bulkUpdates[0].Extra, ModelEvaluationResultsExtraKey)
	require.NotContains(t, repo.bulkUpdates[0].Extra, ModelEvaluationPauseExtraKey)

	created, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{
		ModelEvaluationResultsExtraKey: history, ModelEvaluationPauseExtraKey: map[string]any{"reason": "candy_wrong"},
	})
	require.NoError(t, err)
	require.NotContains(t, created.Extra, ModelEvaluationResultsExtraKey)
	require.NotContains(t, created.Extra, ModelEvaluationPauseExtraKey)
	cloned, err := duplicateAccountExtra(map[string]any{ModelEvaluationResultsExtraKey: history, "custom": "keep"})
	require.NoError(t, err)
	require.NotContains(t, cloned, ModelEvaluationResultsExtraKey)
	require.Equal(t, "keep", cloned["custom"])
}

func TestModelEvaluationHistoryKeepsOnlyFiveNewestResults(t *testing.T) {
	account := &Account{ID: 1, Extra: map[string]any{"custom": "keep"}}
	repo := &modelEvaluationRepo{openAIAccountTestRepo: openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}}}
	svc := &AccountTestService{accountRepo: repo}
	for i := 1; i <= 8; i++ {
		require.NoError(t, svc.saveModelEvaluationResult(context.Background(), 1, ModelEvaluationResult{ID: fmt.Sprint(i), Kind: "candy", Status: "correct", Source: "manual"}))
	}
	history := account.Extra[ModelEvaluationResultsExtraKey].([]ModelEvaluationResult)
	require.Len(t, history, 5)
	require.Equal(t, "8", history[0].ID)
	require.Equal(t, "4", history[4].ID)
	require.Equal(t, "keep", account.Extra["custom"])
}
