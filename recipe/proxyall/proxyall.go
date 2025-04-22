package proxyall

import (
	"context"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp"
	"github.com/caffeine-projects/vlatte/filter"
)

type Config struct {
	Target               string
	Headers              http.Header
	ProxyHeaders         http.Header
	ProxyHeadersToRemove []string
	TrimPrefix           string
	TrimSuffix           string
	SSLVerify            bool
}

func New(config Config) vlatte.Recipe[*xhttp.HTTPMock, *xhttp.HTTPMockApp] {
	return vlatte.
		RecipeFunc[*xhttp.HTTPMock, *xhttp.HTTPMockApp](func(ctx context.Context, pouch *vlatte.Pouch[*xhttp.HTTPMock, *xhttp.HTTPMockApp]) error {

		_, err := pouch.Drink.Brew(xhttp.Request().
			MethodMatches(filter.Anything()).
			Priority(10).
			Reply(xhttp.From(config.Target).
				Headers(config.Headers).
				ProxyHeaders(config.ProxyHeaders).
				RemoveProxyHeaders(config.ProxyHeadersToRemove...).
				TrimPrefix(config.TrimPrefix).
				TrimSuffix(config.TrimSuffix).
				SSLVerify(config.SSLVerify)))

		return err
	})
}
