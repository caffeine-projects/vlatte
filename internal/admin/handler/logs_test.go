package handler_test

import (
	"net/http"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
)

func TestLogs(t *testing.T) {
	// A client follows the server's log lines as the logger writes them, where the server runs out of its sight,
	// such as in a container.
	logs := handler.NewLogs()
	url := serve(t, newStore(t), admin.Logs(logs))

	c := listen(t, url+"/logs")

	logger := zerolog.New(logs)
	logger.Info().Str("url", "/hello").Msg("request received")
	logger.Warn().Msg("webhook failed")

	event, data := c.next(t)
	require.Equal(t, "log", event)
	require.JSONEq(t, `{"level": "info", "url": "/hello", "message": "request received"}`, data)

	_, data = c.next(t)
	require.JSONEq(t, `{"level": "warn", "message": "webhook failed"}`, data)
}

func TestLogs_NotSupported(t *testing.T) {
	// A server that logs through a logger it was given streams no log lines.
	url := serve(t, newStore(t), admin.Logs(nil))

	res, body := send(t, request{method: http.MethodGet, url: url + "/logs"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't stream its logs", doc["detail"])
}
