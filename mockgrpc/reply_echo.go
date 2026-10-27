package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	_ Reply   = (*EchoReply)(nil)
	_ Replier = (*echoReplier)(nil)
)

// EchoReply answers each call with the messages the client sent, as they came: each one's bytes, read as a message of
// the type the method answers with, so the fields of the same numbers come back. A unary or server streaming call
// answers its request, a bidi streaming call each message as it comes, and a client streaming call the last message
// the client sent, or an empty one when it sent none. A message whose bytes don't read as the output type, such as
// bytes that aren't UTF-8 for a text field, fails the call.
type EchoReply struct {
	log bool
}

// Echo creates a new EchoReply.
func Echo() *EchoReply {
	return &EchoReply{}
}

// Log logs each message the reply echoes, with the server's logger, at info level.
func (e *EchoReply) Log() *EchoReply {
	e.log = true
	return e
}

// NewReplier implements Reply.
func (e *EchoReply) NewReplier(app *Server, _ []protoreflect.MethodDescriptor) (Replier, error) {
	r := &echoReplier{}
	if e.log {
		r.logger = app.logger
	}

	return r, nil
}

// echoReplier answers the calls of one mock with their messages, and logs them to logger, when set.
type echoReplier struct {
	logger *zerolog.Logger
}

// Reply implements Replier.
func (e *echoReplier) Reply(_ context.Context, call *Call) error {
	if call.isClientStream() && call.isServerStream() {
		for {
			msg, err := call.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}

			if err != nil {
				return err
			}

			if err := e.echo(call, msg); err != nil {
				return err
			}
		}
	}

	msgs := call.Messages()
	if len(msgs) == 0 {
		return nil
	}

	return e.echo(call, msgs[len(msgs)-1])
}

// echo sends msg back, as a message of the type the method of call answers with.
func (e *echoReplier) echo(call *Call, msg proto.Message) error {
	// The server decoded msg from the wire: it encodes back.
	b, _ := proto.Marshal(msg)

	out := call.NewOutput()
	if err := proto.Unmarshal(b, out); err != nil {
		return fmt.Errorf("reply_echo: the message is no %s: %w", out.ProtoReflect().Descriptor().FullName(), err)
	}

	if e.logger != nil {
		text, err := call.server.resolver.encode(msg)
		if err != nil {
			text = []byte("null")
		}

		e.logger.Info().Str("method", call.Method()).RawJSON("message", text).Err(err).Msg("echo")
	}

	return call.Send(out)
}
