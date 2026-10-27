package vlatte

import (
	"net/http"

	"github.com/rs/zerolog"
)

// Kit is what the mock pipeline gives its components, such as event listeners: the server's dependencies and
// services, and what they serve, never its settings.
type Kit interface {
	// TemplateEngine is the engine of the server, so a component's templates call the same functions as the
	// templates of replies.
	TemplateEngine() TemplateEngine

	// Data is the template data of the request being served.
	Data() any

	// Logger is the server's logger, never nil, so a component logs where the server does, and as little: a server
	// that is silent gives one that logs nothing.
	Logger() *zerolog.Logger

	// Transport is what the server sends HTTP requests through, never nil, so a component's requests, such as
	// webhooks, share its settings and connections, and their connections don't outlive the server.
	Transport() http.RoundTripper

	// Dir is where relative files open from: the directory of the mock file that defines what is served, such as an
	// event, or "" for the working directory, as for what is defined in Go.
	Dir() string
}
