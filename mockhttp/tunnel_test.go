package mockhttp

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTunnel_ASideThatCantEndOnlyWhatItSendsIsClosed(t *testing.T) {
	// A connection without CloseWrite, such as an in-memory one, can't end only what goes out on it. Once its other
	// side ended, the tunnel closes it whole, so its peer still reads to the end, and the tunnel ends.
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = pipe(client, upstream, nil, nil)
	}()

	go func() {
		_, _ = io.WriteString(clientPeer, "the last words")
		_ = clientPeer.Close()
	}()

	got, err := io.ReadAll(upstreamPeer)
	require.NoError(t, err, "the upstream's peer never read to the end")
	require.Equal(t, "the last words", string(got))

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel didn't end")
	}
}
