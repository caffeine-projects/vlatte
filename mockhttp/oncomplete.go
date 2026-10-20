package mockhttp

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.OnComplete = (*OnComplete)(nil)

// OnComplete implements vlatte.OnComplete for HTTP mocks. Listeners get it once the mock replied, and the response
// waits for them. It's read-only: the listeners of a request run at the same time. The request's body was read: read
// it from Request, not from RawRequest.Body. Listeners use the context they get: it has the request's values, and the
// client leaving doesn't end it, as it ends the request's.
type OnComplete struct {
	m         *Mock
	req       *RequestValues
	res       *Response
	kit       vlatte.Kit
	args      vlatte.Parameters
	dir       string
	logger    *zerolog.Logger
	transport http.RoundTripper
	timeout   time.Duration
}

func (i *OnComplete) Kind() vlatte.Kind {
	return Kind
}

func (i *OnComplete) Parameters() vlatte.Parameters {
	return i.args
}

func (i *OnComplete) Kit() vlatte.Kit {
	return i.kit
}

func (i *OnComplete) Mock() vlatte.Mock {
	return i.m
}

func (i *OnComplete) Request() *RequestValues {
	return i.req
}

func (i *OnComplete) Response() *Response {
	return i.res
}

// Dir is where relative files of the event's parameters open from: the directory of the mock file that defines the
// event, or "" for the working directory, as for events defined in Go and global events.
func (i *OnComplete) Dir() string {
	return i.dir
}

// Logger is the server's logger, so listeners such as webhooks log where the server does, and as little.
func (i *OnComplete) Logger() *zerolog.Logger {
	return i.logger
}

// Transport is the server's Config.Transport, or else a transport the server makes and closes with it, so listeners
// such as webhooks send through it when they have none of their own, and their connections don't outlive the server.
func (i *OnComplete) Transport() http.RoundTripper {
	return i.transport
}

// WebhookTimeout is the server's Config.WebhookTimeout, so webhooks without a timeout of their own wait for their
// answer as long as the server says. 0 leaves them to their default.
func (i *OnComplete) WebhookTimeout() time.Duration {
	return i.timeout
}
