package handler

import (
	"bytes"
	"io"
	"net/http"
)

var _ io.Writer = (*Logs)(nil)

// Logs are the log lines of a mock server, which its logger writes and GET /logs streams. It keeps none.
type Logs struct {
	stream *stream
}

// NewLogs creates the logs of a mock server.
func NewLogs() *Logs {
	return &Logs{stream: newStream()}
}

// Write sends p, a log line, to the clients of GET /logs. It never waits for them, and never fails, so the logger it
// writes for never does either.
func (l *Logs) Write(p []byte) (int, error) {
	if l.stream.listening() {
		l.stream.publish("log", bytes.Clone(p))
	}

	return len(p), nil
}

// Close ends the streams of GET /logs.
func (l *Logs) Close() {
	l.stream.close()
}

// LogStream streams the log lines of the mock server, as it writes them, as Server-Sent Events of type log: GET /logs.
func LogStream(logs *Logs) http.Handler {
	return http.HandlerFunc(logs.stream.serve)
}
