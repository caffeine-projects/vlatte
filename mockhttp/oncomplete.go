package mockhttp

import (
	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.OnComplete = (*OnComplete)(nil)

// OnComplete implements vlatte.OnComplete for HTTP mocks. Listeners get it after the response, in the background, so
// it's read-only, and the request has ended: read the request from Request, not from RawRequest.Body or the request's
// context. Uploaded files are there when the built-in body parsers parsed them, in memory; files a custom parser
// wrote to disk are gone.
type OnComplete struct {
	m      *Mock
	req    *RequestValues
	res    *Response
	kit    vlatte.Kit
	args   vlatte.Parameters
	dir    string
	logger *zerolog.Logger
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
