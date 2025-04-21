package xhttp

import (
	"io"
	"net/http"
	"os"

	"github.com/caffeine-projects/vlatte/brewer/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type ReplyEcho struct {
	stdoutLog bool
}

func Echo() *ReplyEcho {
	return &ReplyEcho{}
}

func (e *ReplyEcho) Log() *ReplyEcho {
	e.stdoutLog = true
	return e
}

func (e *ReplyEcho) Build(w http.ResponseWriter, r *RequestValues) (*MockedResponse, error) {
	w.Header().Add(httpval.HeaderContentType, httpval.MIMETextPlainCharsetUTF8)

	if e.stdoutLog {
		return nil, r.RawRequest.Write(io.MultiWriter(w, os.Stdout))
	}

	return nil, r.RawRequest.Write(w)
}

func (e *ReplyEcho) Describe() descriptor.Descriptor {
	return descriptor.New("echo", map[string]any{
		"stdout_log": e.stdoutLog,
	})
}
