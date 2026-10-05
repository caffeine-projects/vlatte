package mockhttp

import (
	"context"
	"io"
	"os"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var _ Replier = (*EchoReply)(nil)

type EchoReply struct {
	stdoutLog bool
}

func Echo() *EchoReply {
	return &EchoReply{}
}

func (e *EchoReply) Log() *EchoReply {
	e.stdoutLog = true
	return e
}

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (e *EchoReply) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	input.Writer.Header().Add(HeaderContentType, MIMETextPlainCharsetUTF8)

	if e.stdoutLog {
		return nil, input.RequestValues.RawRequest.Write(io.MultiWriter(input.Writer, os.Stdout))
	}

	return nil, input.RequestValues.RawRequest.Write(input.Writer)
}

func (e *EchoReply) Describe() descriptor.Descriptor {
	return descriptor.New("echo", map[string]any{
		"stdout_log": e.stdoutLog,
	})
}
