// Package proxyall provides a recipe that prepares an HTTP mock server to send the requests no other mock matches to
// a real service: mock the endpoints a test cares about, and let everything else reach the service as it is.
//
//	err := m.Prep(ctx, proxyall.New(mockhttp.From("https://api.example.com").Timeout(5*time.Second)))
package proxyall

import (
	"context"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// New returns a recipe that proxies the requests no other mock matches with reply, through mockhttp.ProxyAll: every
// option of the reply applies, such as its timeout or its transport.
func New(reply *mockhttp.ProxyReply) vlatte.Recipe[*mockhttp.Mock, *mockhttp.Server] {
	return vlatte.
		RecipeFunc[*mockhttp.Mock, *mockhttp.Server](func(_ context.Context, input *vlatte.RecipeInput[*mockhttp.Mock, *mockhttp.Server]) error {
		_, err := input.Server.Mock(mockhttp.ProxyAll(reply))
		return err
	})
}
