package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

var errAccountEvaluationProxy = errors.New("the account proxy is unavailable or changed; reload the account and retry")

type accountEvaluationProxyKey struct{}

type accountEvaluationProxy struct {
	accountID int64
	proxyID   int64
	url       string
}

// bindAccountEvaluationProxy pins the credential owner's configured route for
// both authentication and the diagnostic request. Missing proxy relations must
// not turn an explicitly proxied account into a direct connection.
func bindAccountEvaluationProxy(ctx context.Context, account *Account) (context.Context, string, error) {
	if account == nil {
		return ctx, "", errAccountEvaluationProxy
	}
	binding := accountEvaluationProxy{accountID: account.ID}
	if account.ProxyID != nil {
		proxy := account.Proxy
		if *account.ProxyID <= 0 || proxy == nil || proxy.ID != *account.ProxyID || strings.TrimSpace(proxy.Host) == "" || proxy.Port < 1 || proxy.Port > 65535 {
			return ctx, "", errAccountEvaluationProxy
		}
		proxyURL, parsed, err := proxyurl.Parse(proxy.URL())
		if err != nil || parsed == nil {
			return ctx, "", errAccountEvaluationProxy
		}
		binding.proxyID = *account.ProxyID
		binding.url = proxyURL
	}
	return context.WithValue(ctx, accountEvaluationProxyKey{}, binding), binding.url, nil
}

// accountEvaluationProxyFromContext survives the OAuth refresh API's account
// re-read. A concurrent proxy reassignment aborts the diagnostic; it never
// silently switches the refresh to direct access or a different proxy.
func accountEvaluationProxyFromContext(ctx context.Context, account *Account) (string, bool, error) {
	binding, bound := ctx.Value(accountEvaluationProxyKey{}).(accountEvaluationProxy)
	if !bound {
		return "", false, nil
	}
	if account == nil || account.ID != binding.accountID {
		return "", true, errAccountEvaluationProxy
	}
	var proxyID int64
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	if proxyID != binding.proxyID {
		return "", true, errAccountEvaluationProxy
	}
	return binding.url, true, nil
}

func hasAccountEvaluationProxy(ctx context.Context) bool {
	_, bound := ctx.Value(accountEvaluationProxyKey{}).(accountEvaluationProxy)
	return bound
}

// AccountEvaluationUsesDirectConnection tells OAuth transports to explicitly
// disable environment proxies for a diagnostic account configured for direct
// access. Ordinary gateway and refresh contexts retain their existing policy.
func AccountEvaluationUsesDirectConnection(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	binding, bound := ctx.Value(accountEvaluationProxyKey{}).(accountEvaluationProxy)
	return bound && binding.proxyID == 0
}
