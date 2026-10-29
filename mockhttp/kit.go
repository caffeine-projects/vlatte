package mockhttp

import (
	"net/http"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Kit = (*Kit)(nil)

// Kit is the vlatte.Kit of HTTP mocks: the server's template engine, logger and webhook transport, the template data
// of the request, and the directory of the mock file of the event it serves.
type Kit struct {
	engine    vlatte.TemplateEngine
	data      any
	logger    *zerolog.Logger
	transport http.RoundTripper
	dir       string
}

// TemplateEngine implements [vlatte.Kit]: the server's template engine.
func (k *Kit) TemplateEngine() vlatte.TemplateEngine {
	return k.engine
}

// Data implements [vlatte.Kit]: the template data of the request, as the templates of its reply read it.
func (k *Kit) Data() any {
	return k.data
}

// Logger implements vlatte.Kit: the server's logger.
func (k *Kit) Logger() *zerolog.Logger {
	return k.logger
}

// Transport implements vlatte.Kit: the server's Config.Transport, or else a transport the server makes and closes
// with it.
func (k *Kit) Transport() http.RoundTripper {
	return k.transport
}

// Dir implements vlatte.Kit: the directory of the mock file that defines the event, or "" for the working directory,
// as for events defined in Go and global events.
func (k *Kit) Dir() string {
	return k.dir
}
