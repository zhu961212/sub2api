package repository

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEvaluationDirectReqClientDisablesEnvironmentProxyWithoutMutatingSharedClient(t *testing.T) {
	ordinaryOptions := reqClientOptions{Timeout: 123 * time.Second}
	ordinary, err := getSharedReqClient(ordinaryOptions)
	require.NoError(t, err)
	require.NotNil(t, ordinary.GetTransport().Proxy)
	directOptions := ordinaryOptions
	directOptions.DisableEnvironmentProxy = true
	direct, err := getSharedReqClient(directOptions)
	require.NoError(t, err)
	require.NotSame(t, ordinary, direct)
	require.Nil(t, direct.GetTransport().Proxy, "explicit direct diagnostics cannot consult HTTP_PROXY/HTTPS_PROXY")
	ordinaryAgain, err := getSharedReqClient(ordinaryOptions)
	require.NoError(t, err)
	require.Same(t, ordinary, ordinaryAgain)
	require.NotNil(t, ordinaryAgain.GetTransport().Proxy, "ordinary clients keep their existing environment proxy behavior")
	directAgain, err := getSharedReqClient(directOptions)
	require.NoError(t, err)
	require.Same(t, direct, directAgain, "repeated diagnostics still reuse their transport")
}

func TestEvaluationReqClientConfiguredProxyTakesPrecedence(t *testing.T) {
	client, err := getSharedReqClient(reqClientOptions{
		Timeout: 123 * time.Second, ProxyURL: "http://user:secret@proxy.example:8080", DisableEnvironmentProxy: true,
	})
	require.NoError(t, err)
	require.NotNil(t, client.GetTransport().Proxy)
	request, err := http.NewRequest(http.MethodPost, "https://auth.openai.com/oauth/token", nil)
	require.NoError(t, err)
	proxy, err := client.GetTransport().Proxy(request)
	require.NoError(t, err)
	require.Equal(t, "http://user:secret@proxy.example:8080", proxy.String())
}
