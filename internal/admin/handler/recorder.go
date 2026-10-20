package handler

import (
	_ "embed"
	"net/http"
)

// _recorderPage starts and stops recordings through GET, POST and DELETE /recording.
//
//go:embed recorder.html
var _recorderPage []byte

// RecorderPage answers the page that records a service: GET /recorder. On a server that can't record, it says so.
func RecorderPage() http.Handler {
	return page(_recorderPage)
}
