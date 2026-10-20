package httpx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWait_EndsWithTheContext(t *testing.T) {
	// A delay ends when the client goes away, or the server closes, and doesn't sleep for nothing.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()
	require.ErrorIs(t, Wait(ctx, time.Hour), context.Canceled)
	require.Less(t, time.Since(start), time.Minute)

	require.NoError(t, Wait(t.Context(), time.Millisecond))
}

func TestIsToken(t *testing.T) {
	// Methods and header names are tokens: a space, a separator or a control character would let them break the
	// request line or the header they go in.
	for _, s := range []string{"GET", "post", "X-Custom_Header", "M-SEARCH", "!#$%&'*+-.^_`|~"} {
		require.True(t, IsToken(s), s)
	}

	for _, s := range []string{"", "GET /", "X:Y", "a\r\nb", "é", "(x)", "a\x7f"} {
		require.False(t, IsToken(s), s)
	}
}
