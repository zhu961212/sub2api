package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHTTPUpstreamOpenAIProxyFailureNeverFallsBackToDirect(t *testing.T) {
	var directRequests atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(origin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	proxy.Close()
	ctx, cancel := context.WithTimeout(service.WithHTTPUpstreamProfile(t.Context(), service.HTTPUpstreamProfileOpenAI), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL, nil)
	require.NoError(t, err)
	upstream, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	response, err := upstream.DoWithTLS(request, proxy.URL, 41, 1, nil)
	require.Error(t, err)
	require.Nil(t, response)
	require.Zero(t, directRequests.Load())
	upstream.mu.RLock()
	defer upstream.mu.RUnlock()
	for _, client := range upstream.clients {
		require.NotEqual(t, directProxyKey, client.proxyKey)
		require.Zero(t, atomic.LoadInt64(&client.inFlight), "failed diagnostics must release their pooled transport slot")
	}
}
