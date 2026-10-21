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
		_, err := input.Flavor.Mock(mockhttp.ProxyAll(reply))
		return err
	})
}
