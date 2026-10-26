package mockhttp

import (
	"context"
	"io"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ ReplierFactory = (*EchoReply)(nil)
	_ Replier        = (*echoReplier)(nil)
)

// EchoReply configures a reply that writes the request back, as it came. It is a ReplierFactory.
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

func (e *EchoReply) Describe() descriptor.Descriptor {
	return descriptor.New("echo", map[string]any{
		"stdout_log": e.stdoutLog,
	})
}

// NewReplier implements ReplierFactory.
func (e *EchoReply) NewReplier(app *Server) (Replier, error) {
	return &echoReplier{log: e.stdoutLog, out: app.logOutput()}, nil
}

// echoReplier writes the requests of one mock back, as they came, and to out, the server's log output, when it logs.
type echoReplier struct {
	log bool
	out io.Writer
}

// Reply implements Replier.
func (e *echoReplier) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	input.Writer.Header().Add(HeaderContentType, MIMETextPlainCharsetUTF8)

	if e.log {
		return nil, input.RequestValues.RawRequest.Write(io.MultiWriter(input.Writer, e.out))
	}

	return nil, input.RequestValues.RawRequest.Write(input.Writer)
}
