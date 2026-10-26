package mockgrpc

import (
	"errors"
	"io"
	"slices"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	errSentTwice = errors.New("reply: the call answers one message: Send was called twice")
	errNoRecv    = errors.New("reply: only a bidi streaming call receives messages: its messages are in Messages")
)

// Call is a call a mock serves, as its matchers, replies, templates and events read it.
//
// A unary or server streaming call has one request message, in Message. A client streaming call has all the messages
// the client sent, in Messages, once it closed its side. A bidi streaming call has the messages received so far: its
// mock matches before any, on the method and the header, and its reply reads them with Recv.
//
// Its methods are safe for concurrent use, but Send and Recv, which follow grpc's streams: one goroutine at a time may
// call Send, and one Recv. The messages are the server's: don't change them.
type Call struct {
	server *Server
	method string
	header metadata.MD
	desc   protoreflect.MethodDescriptor
	stream grpc.ServerStream // nil for a unary call

	json []byte // the request as JSON, as matchers read it: set as the call is served

	mu       sync.Mutex
	message  proto.Message
	messages []proto.Message
	sent     []proto.Message
	answer   proto.Message // what Send set, for a call that answers one message
}

// newCall returns the call of method, which md describes, with header, the metadata of the client.
func newCall(app *Server, method string, md protoreflect.MethodDescriptor, header metadata.MD, stream grpc.ServerStream) *Call {
	return &Call{server: app, method: method, desc: md, header: header, stream: stream}
}

// Server returns the server serving the call.
func (c *Call) Server() *Server {
	return c.server
}

// Method returns the full method of the call, such as "/pkg.Service/Method".
func (c *Call) Method() string {
	return c.method
}

// Header returns the metadata the client sent.
func (c *Call) Header() metadata.MD {
	return c.header
}

// Descriptor returns the descriptor of the method of the call.
func (c *Call) Descriptor() protoreflect.MethodDescriptor {
	return c.desc
}

// Message returns the request message of a unary or server streaming call, nil for other calls.
func (c *Call) Message() proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.message
}

// Messages returns the messages the client sent so far, in order: one for a unary or server streaming call.
func (c *Call) Messages() []proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.message != nil {
		return []proto.Message{c.message}
	}

	return slices.Clip(c.messages)
}

// Sent returns the messages the mock sent so far, in order.
func (c *Call) Sent() []proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clip(c.sent)
}

// JSON returns the request as JSON, as matchers, the journal and templates read it: the message of a unary or server
// streaming call, as protojson writes it with the field names of the .proto file, and an array of the messages of a
// streaming client, received so far.
func (c *Call) JSON() []byte {
	b, _ := c.encode()
	return b
}

// NewOutput returns a new, empty message of the type the method of the call answers with: a reply that builds its
// answer, such as Func's, fills it in.
func (c *Call) NewOutput() proto.Message {
	return newMessage(c.desc.Output())
}

// Send sends msg to the client. A unary or client streaming call answers one message: Send sets it, and the call
// sends it once the reply returns. A streaming server sends each message as Send gets it.
func (c *Call) Send(msg proto.Message) error {
	if c.isServerStream() {
		if err := c.stream.SendMsg(msg); err != nil {
			return err
		}

		c.mu.Lock()
		c.sent = append(c.sent, msg)
		c.mu.Unlock()

		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.answer != nil {
		return errSentTwice
	}

	c.answer = msg
	c.sent = append(c.sent, msg)

	return nil
}

// Recv receives the next message of a bidi streaming call, and returns io.EOF once the client closed its side.
// Other calls received their messages before the mock matched: see Message and Messages.
func (c *Call) Recv() (proto.Message, error) {
	if !c.isClientStream() || !c.isServerStream() {
		return nil, errNoRecv
	}

	msg := newMessage(c.desc.Input())
	if err := c.stream.RecvMsg(msg); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.messages = append(c.messages, msg)
	c.mu.Unlock()

	return msg, nil
}

// encode returns the request as JSON, as JSON does, or the error of a message protojson can't write, such as an Any
// of a type the server doesn't know.
func (c *Call) encode() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isClientStream() {
		return c.server.resolver.encode(c.message)
	}

	return c.server.resolver.encodeArray(c.messages)
}

// receive reads the request of a streaming call before matching: the one message of a server streaming call, every
// message of a client streaming one. A bidi call receives its messages as its reply reads them.
func (c *Call) receive() error {
	switch typeOf(c.desc) {
	case _serverStream, _unary:
		msg := newMessage(c.desc.Input())
		if err := c.stream.RecvMsg(msg); err != nil {
			return err
		}

		c.message = msg
	case _clientStream:
		for {
			msg := newMessage(c.desc.Input())

			err := c.stream.RecvMsg(msg)
			if errors.Is(err, io.EOF) {
				return nil
			}

			if err != nil {
				return err
			}

			c.messages = append(c.messages, msg)
		}
	case _bidiStream:
	}

	return nil
}

// answered returns the message Send set, or a new empty one of the output type when the reply set none.
func (c *Call) answered() proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.answer == nil {
		return c.NewOutput()
	}

	return c.answer
}

func (c *Call) isClientStream() bool {
	return c.desc.IsStreamingClient()
}

func (c *Call) isServerStream() bool {
	return c.desc.IsStreamingServer()
}
