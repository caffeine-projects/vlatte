package adminapi_test

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestLogs(t *testing.T) {
	// A client follows the server's log lines as the logger writes them, where the server runs out of its sight,
	// such as in a container.
	logs := adminapi.NewLogs()
	c := listen(t, serveStream(t, logs))

	logger := zerolog.New(logs)
	logger.Info().Str("url", "/hello").Msg("request received")
	logger.Warn().Msg("webhook failed")

	event, data := c.next(t)
	require.Equal(t, "log", event)
	require.JSONEq(t, `{"level": "info", "url": "/hello", "message": "request received"}`, data)

	_, data = c.next(t)
	require.JSONEq(t, `{"level": "warn", "message": "webhook failed"}`, data)
}
