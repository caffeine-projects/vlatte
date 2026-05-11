package xhttp

import (
	"context"
	"io"
	"os"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/flavor/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

var _ vlatte.Replier[ReplyInput, *Response] = (*ReplyEcho)(nil)

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

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (e *ReplyEcho) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	input.Writer.Header().Add(httpval.HeaderContentType, httpval.MIMETextPlainCharsetUTF8)

	if e.stdoutLog {
		return nil, input.RequestValues.RawRequest.Write(io.MultiWriter(input.Writer, os.Stdout))
	}

	return nil, input.RequestValues.RawRequest.Write(input.Writer)
}

func (e *ReplyEcho) Describe() descriptor.Descriptor {
	return descriptor.New("echo", map[string]any{
		"stdout_log": e.stdoutLog,
	})
}
