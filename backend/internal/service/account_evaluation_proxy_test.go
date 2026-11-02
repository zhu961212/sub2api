//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type evaluationProxyBody struct {
	io.Reader
	closed bool
}

func (b *evaluationProxyBody) Close() error { b.closed = true; return nil }

type evaluationProxyUpstream struct {
	urls       []string
	accountIDs []int64
	body       *evaluationProxyBody
	err        error
}

func (u *evaluationProxyUpstream) Do(request *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	u.urls = append(u.urls, proxyURL)
	u.accountIDs = append(u.accountIDs, accountID)
	if u.err != nil {
		return nil, u.err
	}
	u.body = &evaluationProxyBody{Reader: strings.NewReader("diagnostic response")}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: u.body}, nil
}

func (u *evaluationProxyUpstream) DoWithTLS(request *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(request, proxyURL, accountID, concurrency)
}

func evaluationProxyAccount() *Account {
	proxyID := int64(17)
	return &Account{
		ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-access-token"},
		ProxyID:     &proxyID,
		Proxy:       &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 8080, Username: "user", Password: "private-password"},
	}
}

func TestAccountEvaluationUsesConfiguredProxyAndClosesBody(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent", true: "shadow uses current parent proxy"}[shadow], func(t *testing.T) {
			parent := evaluationProxyAccount()
			account := parent
			if shadow {
				account = &Account{ID: 8, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Concurrency: 1}
				// Shadows inherit the parent's proxy. An old shadow snapshot with
				// no proxy relation must not cause a direct diagnostic request.
			}
			repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: parent}}}
			upstream := &evaluationProxyUpstream{}
			svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
			c, _ := newTestContext()
			var observedOwner *Account
			err := svc.testOpenAIAccountConnectionWithOptions(c, account, "gpt-5.4", "", AccountTestModeDefault, openAIAccountTestOptions{
				readOnly: true, suppressStart: true,
				onCredentialAccount: func(owner *Account) { observedOwner = owner },
				processStream:       func(_ *gin.Context, body io.Reader) error { _, err := io.Copy(io.Discard, body); return err },
			})
			require.NoError(t, err)
			require.Equal(t, []string{parent.Proxy.URL()}, upstream.urls)
			require.Equal(t, []int64{account.ID}, upstream.accountIDs)
			require.Equal(t, parent.ID, observedOwner.ID)
			require.True(t, upstream.body.closed)
		})
	}
}

func TestAccountEvaluationMissingOrInvalidProxyFailsBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Account)
	}{
		{"missing relation", func(a *Account) { a.Proxy = nil }},
		{"mismatched relation", func(a *Account) { a.Proxy.ID++ }},
		{"invalid scheme", func(a *Account) { a.Proxy.Protocol = "unsupported" }},
		{"empty host", func(a *Account) { a.Proxy.Host = "" }},
		{"invalid port", func(a *Account) { a.Proxy.Port = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := evaluationProxyAccount()
			tc.edit(account)
			upstream := &evaluationProxyUpstream{}
			svc := &AccountTestService{httpUpstream: upstream}
			c, recorder := newTestContext()
			err := svc.testOpenAIAccountConnectionWithOptions(c, account, "gpt-5.4", "", AccountTestModeDefault, openAIAccountTestOptions{readOnly: true})
			require.ErrorIs(t, err, errAccountEvaluationProxy)
			require.Empty(t, upstream.urls)
			require.NotContains(t, err.Error(), "private-password")
			require.NotContains(t, recorder.Body.String(), "private-password")
		})
	}
}

func TestAccountEvaluationProxyFailureDoesNotRetryDirect(t *testing.T) {
	account := evaluationProxyAccount()
	upstream := &evaluationProxyUpstream{err: errors.New("proxy failed")}
	svc := &AccountTestService{httpUpstream: upstream}
	c, _ := newTestContext()
	err := svc.testOpenAIAccountConnectionWithOptions(c, account, "gpt-5.4", "", AccountTestModeDefault, openAIAccountTestOptions{readOnly: true})
	require.Error(t, err)
	require.Equal(t, []string{account.Proxy.URL()}, upstream.urls)
}

type evaluationProxyOAuthClient struct {
	OpenAIOAuthClient
	urls     []string
	err      error
	response *openai.TokenResponse
}

func (c *evaluationProxyOAuthClient) RefreshTokenWithClientID(_ context.Context, _, proxyURL, _ string) (*openai.TokenResponse, error) {
	c.urls = append(c.urls, proxyURL)
	if c.err != nil {
		return nil, c.err
	}
	if c.response != nil {
		return c.response, nil
	}
	return nil, errors.New("test refresh failure")
}

type evaluationProxyRepository struct {
	ProxyRepository
	reads int
}

func (r *evaluationProxyRepository) GetByID(context.Context, int64) (*Proxy, error) {
	r.reads++
	return nil, errors.New("proxy relation read failed")
}

func TestAccountEvaluationOAuthRefreshUsesSamePinnedProxy(t *testing.T) {
	account := evaluationProxyAccount()
	account.Credentials["refresh_token"] = "test-refresh-token"
	ctx, proxyURL, err := bindAccountEvaluationProxy(context.Background(), account)
	require.NoError(t, err)
	client := &evaluationProxyOAuthClient{}
	proxyRepo := &evaluationProxyRepository{}
	svc := NewOpenAIOAuthService(proxyRepo, client)
	t.Cleanup(svc.Stop)
	_, err = svc.RefreshAccountToken(ctx, account)
	require.EqualError(t, err, "test refresh failure")
	require.Equal(t, []string{proxyURL}, client.urls)
	require.Zero(t, proxyRepo.reads, "refresh reuses the validated route instead of falling back when a second lookup fails")

	changed := *account
	changed.ProxyID = nil
	_, err = svc.RefreshAccountToken(ctx, &changed)
	require.ErrorIs(t, err, errAccountEvaluationProxy)
	require.Len(t, client.urls, 1, "a changed proxy must not initiate direct refresh")
}

func TestAccountEvaluationDoesNotContinueAfterTokenRefreshFailure(t *testing.T) {
	for _, failure := range []string{"test refresh failure", "invalid_grant", "token refresh failed: status 429"} {
		t.Run(failure, func(t *testing.T) {
			account := evaluationProxyAccount()
			account.Credentials["refresh_token"] = "test-refresh-token"
			account.Credentials["expires_at"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
			ctx, _, err := bindAccountEvaluationProxy(context.Background(), account)
			require.NoError(t, err)
			repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}}
			client := &evaluationProxyOAuthClient{err: errors.New(failure)}
			oauth := NewOpenAIOAuthService(nil, client)
			t.Cleanup(oauth.Stop)
			provider := NewOpenAITokenProvider(repo, nil, oauth)
			blocker := &runtimeBlockRecorder{}
			provider.SetAccountRuntimeBlocker(blocker)
			provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, nil), NewOpenAITokenRefresher(oauth, repo))
			token, err := provider.GetAccessToken(ctx, account)
			require.ErrorContains(t, err, failure)
			require.Empty(t, token, "diagnostics must not hide refresh failure by using the old token")
			require.Equal(t, []string{account.Proxy.URL()}, client.urls)
			require.Zero(t, repo.setErrorID)
			require.Zero(t, repo.rateLimitedID)
			require.Zero(t, repo.clearedErrorID)
			require.Empty(t, blocker.accounts)
			require.Empty(t, blocker.clearedIDs)
		})
	}
}

func TestAccountEvaluationExpiredTokenDoesNotDisableAccount(t *testing.T) {
	account := evaluationProxyAccount()
	account.Credentials["expires_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	ctx, _, err := bindAccountEvaluationProxy(context.Background(), account)
	require.NoError(t, err)
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}}
	provider := NewOpenAITokenProvider(repo, nil, nil)
	blocker := &runtimeBlockRecorder{}
	provider.SetAccountRuntimeBlocker(blocker)
	token, err := provider.GetAccessToken(ctx, account)
	require.ErrorContains(t, err, "refresh_token is missing")
	require.Empty(t, token)
	require.Zero(t, repo.setErrorID)
	require.Empty(t, blocker.accounts)
}

func TestAccountEvaluationAgentRegistrationFailureUsesProxyWithoutDisabling(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	var proxyCalls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		require.Equal(t, "agent.invalid", r.URL.Host)
		require.Equal(t, "/v1/agent/runtime-test/task/register", r.URL.Path)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(proxy.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	previousBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = "http://agent.invalid"
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = previousBase })
	account := evaluationProxyAccount()
	account.Proxy.Host, account.Proxy.Port = host, port
	account.Credentials = map[string]any{"auth_mode": OpenAIAuthModeAgentIdentity, "agent_runtime_id": key.runtimeID, "agent_private_key": privateKey}
	require.True(t, account.IsOpenAIAgentIdentity())
	repo := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}}
	upstream := &evaluationProxyUpstream{}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	c, _ := newTestContext()
	err = svc.testOpenAIAccountConnectionWithOptions(c, account, "gpt-5.4", "", AccountTestModeDefault, openAIAccountTestOptions{readOnly: true})
	require.Error(t, err)
	require.EqualValues(t, 1, proxyCalls.Load())
	require.Empty(t, upstream.urls)
	require.Zero(t, repo.setErrorID)
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Empty(t, repo.updatedExtra)
}

func TestAccountEvaluationProxyBindingPreservesExplicitDirectAndRemoteDNS(t *testing.T) {
	direct := evaluationProxyAccount()
	direct.ProxyID, direct.Proxy = nil, nil
	ctx, route, err := bindAccountEvaluationProxy(context.Background(), direct)
	require.NoError(t, err)
	require.Empty(t, route)
	require.True(t, AccountEvaluationUsesDirectConnection(ctx))
	got, bound, err := accountEvaluationProxyFromContext(ctx, direct)
	require.NoError(t, err)
	require.True(t, bound)
	require.Empty(t, got)

	proxied := evaluationProxyAccount()
	proxied.Proxy.Protocol = "socks5"
	ctx, route, err = bindAccountEvaluationProxy(context.Background(), proxied)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(route, "socks5h://"))
	require.False(t, AccountEvaluationUsesDirectConnection(ctx))
	require.False(t, AccountEvaluationUsesDirectConnection(context.Background()))
}

func TestAccountEvaluationTokenRefreshSkipsOptionalPrivacyRequests(t *testing.T) {
	account := evaluationProxyAccount()
	account.Credentials["refresh_token"] = "old-refresh"
	ctx, _, err := bindAccountEvaluationProxy(context.Background(), account)
	require.NoError(t, err)
	client := &evaluationProxyOAuthClient{response: &openai.TokenResponse{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}}
	svc := NewOpenAIOAuthService(nil, client)
	t.Cleanup(svc.Stop)
	privacyCalls := 0
	svc.SetPrivacyClientFactory(func(string) (*req.Client, error) {
		privacyCalls++
		return nil, errors.New("unexpected privacy request")
	})
	result, err := svc.RefreshAccountToken(ctx, account)
	require.NoError(t, err)
	require.Equal(t, "new-access", result.AccessToken)
	require.Zero(t, privacyCalls)
	require.Equal(t, []string{account.Proxy.URL()}, client.urls)
}

type evaluationProxyPluginStream struct {
	grpc.ClientStream
	start *pluginv1.ForwardRequestStart
}

func (s *evaluationProxyPluginStream) Send(frame *pluginv1.ForwardRequest) error {
	s.start = frame.GetStart()
	return errors.New("stop after request metadata")
}

func (*evaluationProxyPluginStream) Recv() (*pluginv1.ForwardResponse, error) { return nil, io.EOF }

type evaluationProxyPluginClient struct {
	pluginv1.TransportPluginClient
	stream *evaluationProxyPluginStream
}

func (c *evaluationProxyPluginClient) Forward(context.Context, ...grpc.CallOption) (pluginv1.TransportPlugin_ForwardClient, error) {
	return c.stream, nil
}

func TestAccountEvaluationPluginMetadataCarriesConfiguredProxy(t *testing.T) {
	account := evaluationProxyAccount()
	ctx, proxyURL, err := bindAccountEvaluationProxy(context.Background(), account)
	require.NoError(t, err)
	stream := &evaluationProxyPluginStream{}
	runtime := &pluginRuntime{api: &evaluationProxyPluginClient{stream: stream}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader("{}"))
	require.NoError(t, err)
	_, err = runtime.roundTrip(ctx, request, proxyURL, account)
	require.Error(t, err)
	require.NotNil(t, stream.start)
	require.Equal(t, proxyURL, stream.start.ProxyUrl)
	require.Equal(t, account.ID, stream.start.AccountId)
}

func TestAccountEvaluationUnavailablePluginDoesNotFallBack(t *testing.T) {
	account := evaluationProxyAccount()
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "not running"})
	upstream := &evaluationProxyUpstream{}
	svc := &AccountTestService{pluginManager: manager, httpUpstream: upstream}
	c, _ := newTestContext()
	err := svc.testOpenAIAccountConnectionWithOptions(c, account, "gpt-5.4", "", AccountTestModeDefault, openAIAccountTestOptions{readOnly: true})
	require.Error(t, err)
	require.Empty(t, upstream.urls)
}
