package e2e

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestOnComplete_ListenersReceiveTheRequestAndServerDependencies(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "tea.mock.yaml")
	require.NoError(t, os.WriteFile(file, []byte(`id: tea
request:
  method: POST
  path: /tea
response:
  status: 201
  header_template:
    X-Request-ID: '{{ .Request.ID }}'
on_complete:
  - name: capture
`), 0o600))

	got := make(chan vlatte.OnComplete, 1)
	listener := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, event vlatte.OnComplete) error {
		got <- event
		return nil
	})
	logger := zerolog.Nop()
	transport := &http.Transport{}
	m := mockhttp.MustNew(mockhttp.Setup().Logger(&logger).Transport(transport).OnCompleteListener("capture", listener))
	require.Same(t, &logger, m.Logger())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.FromFile(file))

	before := time.Now()
	res, err := http.Post(m.URL("/tea"), mockhttp.MIMETextPlain, strings.NewReader("tea"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	_, err = io.Copy(io.Discard, res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	select {
	case event := <-got:
		httpEvent, ok := event.(*mockhttp.OnComplete)
		require.True(t, ok)
		require.Equal(t, dir, httpEvent.Dir())
		require.Same(t, &logger, httpEvent.Logger())
		require.Same(t, transport, httpEvent.Transport())
		request := httpEvent.Request()
		require.NotEmpty(t, res.Header.Get("X-Request-ID"))
		require.Equal(t, res.Header.Get("X-Request-ID"), request.ID())
		require.WithinRange(t, request.StartedAt(), before, time.Now())
		require.Equal(t, http.MethodPost, request.OriginalRequest().Method)
		require.Equal(t, "/tea", request.OriginalRequest().URL.Path)
		require.Equal(t, []byte("tea"), request.RawBody())
		require.Equal(t, "tea", request.ParsedBody())
	default:
		t.Fatal("the response arrived before the listener received its event")
	}
}
