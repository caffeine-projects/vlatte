package proxyall

import (
	"context"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
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

func New(config Config) vlatte.Recipe[*mockhttp.Mock, *mockhttp.Server] {
	return vlatte.
		RecipeFunc[*mockhttp.Mock, *mockhttp.Server](func(ctx context.Context, input *vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server]) error {
		_, err := input.Flavor.Mock(mockhttp.Request().
			MethodMatches(match.Anything()).
			Priority(10).
			Reply(mockhttp.From(config.Target).
				Headers(config.Headers).
				ProxyHeaders(config.ProxyHeaders).
				RemoveProxyHeaders(config.ProxyHeadersToRemove...).
				TrimPrefix(config.TrimPrefix).
				TrimSuffix(config.TrimSuffix).
				SSLVerify(config.SSLVerify)))

		return err
	})
}
