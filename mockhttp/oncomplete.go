package mockhttp

import (
	"net/http"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.OnComplete = (*OnComplete)(nil)

// OnComplete implements vlatte.OnComplete for HTTP mocks. Listeners, and the callbacks of [Builder.Callback], get it
// once the mock replied, and the response waits for them, but for those set to run after it. It's read-only: the listeners of a request run at the same
// time. The request's body was read: read it from Request, not from RawRequest.Body. Listeners use the context they get: it has the request's values, and the
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
}

// Kind implements [vlatte.EventArgs]: [Kind], for HTTP mocks.
func (i *OnComplete) Kind() vlatte.Kind {
	return Kind
}

// Parameters implements [vlatte.EventArgs]: the parameters of the event, as its listener prepared them.
func (i *OnComplete) Parameters() vlatte.Parameters {
	return i.args
}

// Kit implements [vlatte.EventArgs]: a [*Kit].
func (i *OnComplete) Kit() vlatte.Kit {
	return i.kit
}

// Mock implements [vlatte.OnComplete]: the [*Mock] that replied.
func (i *OnComplete) Mock() vlatte.Mock {
	return i.m
}

// Request returns the request the mock replied to.
func (i *OnComplete) Request() *RequestValues {
	return i.req
}

// Response returns the response the mock replied with, or nil for a reply that wrote its answer itself, such as
// [Echo].
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
