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

	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

var (
	errSentTwice = errors.New("reply: the call answers one message: Send was called twice")
	errNoRecv    = errors.New("reply: only a bidi streaming call receives messages: its messages are in Messages")
)

// Call is a call a mock serves, as its matchers, replies, templates and events read it.
//
// A unary or server streaming call has one request message, in Message. A client streaming call has all the messages
// the client sent, in Messages, once it closed its side. A bidi streaming call has the messages received so far: its
// mock matches before any, on the method and the header, and its reply reads them with Recv. Message and Messages are
// the messages as the client sent them; Parsed is what the server's [RequestParser] values read of them.
//
// Its methods are safe for concurrent use, but Send and Recv, which follow grpc's streams: one goroutine at a time may
// call Send, and one Recv. The messages are the server's: don't change them.
type Call struct {
	server *Server
	method string
	header metadata.MD
	desc   protoreflect.MethodDescriptor
	stream grpc.ServerStream // nil for a unary call

	json []byte // the request as JSON, as matchers read it, parsed: set as the call is served

	// data and provided are what templates read as .Data and .Provided: set once a mock matched the call.
	data     map[string]any
	provided map[string]any

	// scenarioMoves are the moves of the scenarios of the mock that matched the call, which its templates read.
	scenarioMoves []matchscenario.Move

	mu       sync.Mutex
	message  proto.Message
	messages []proto.Message
	parsed   []parsedMessage // what the server's parsers read of the request messages, in order: a prefix of them
	sent     []proto.Message
	answer   proto.Message // what Send set, for a call that answers one message
	hit      *hit          // counts the call as its answer goes out
	proxied  bool          // the answer is a proxy's: another server answered
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

	return c.received()
}

// Parsed returns what the server's [RequestParser] values read of the messages the client sent so far, in order, one
// for each of Messages: the value of the parser that parsed a message, or the message itself when none did. The value
// of a message whose parsing failed is nil. A bidi message a proxy or [Echo] received isn't parsed. Matchers and
// templates read these values as JSON. Don't change them.
func (c *Call) Parsed() []any {
	c.mu.Lock()
	defer c.mu.Unlock()

	msgs := c.received()
	values := make([]any, len(msgs))
	for i, msg := range msgs {
		values[i] = c.parsedAt(i, msg).value
	}

	return values
}

// Sent returns the messages the mock sent so far, in order.
func (c *Call) Sent() []proto.Message {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clip(c.sent)
}

// JSON returns the request as JSON, as the journal keeps it: the message of a unary or server streaming call, as
// protojson writes it with the field names of the .proto file, and an array of the messages of a streaming client,
// received so far. It is the request as the client sent it: [Builder.Field], [Builder.Body] and templates read what the
// server's [RequestParser] values read of it, written the same way. Without parsers, they read this JSON.
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
		// SendMsg sends the header first.
		c.hit.count()

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
//
// The server's [RequestParser] values parse the message as it comes, and [Call.Parsed] then has what they read: Recv
// returns the message as the client sent it. A message a parser failed to parse returns an error that wraps
// [ErrParsing] and the parser's error, whose gRPC status, when it has one, is the answer the client gets when the reply
// returns it.
func (c *Call) Recv() (proto.Message, error) {
	msg, _, err := c.recvNext(true)
	return msg, err
}

// recvNext receives the next message of a bidi streaming call, as Recv does, and returns what the server's parsers read
// of it. Without parse, it doesn't parse the message, for replies that forward it as it came, such as proxies and Echo.
func (c *Call) recvNext(parse bool) (proto.Message, parsedMessage, error) {
	if !c.isClientStream() || !c.isServerStream() {
		return nil, parsedMessage{}, errNoRecv
	}

	msg := newMessage(c.desc.Input())
	if err := c.stream.RecvMsg(msg); err != nil {
		return nil, parsedMessage{}, err
	}

	p := parsedMessage{value: msg, json: nil}

	var err error
	if parse {
		c.mu.Lock()
		i := len(c.messages)
		c.mu.Unlock()

		p, err = c.server.parse(c.stream.Context(), c, msg, i)
	}

	// The message and what the parsers read of it come in together: Parsed has one value for each of Messages.
	c.mu.Lock()
	c.messages = append(c.messages, msg)
	c.parsed = append(c.parsed, p)
	c.mu.Unlock()

	if err != nil {
		return nil, parsedMessage{}, err
	}

	return msg, p, nil
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

// view returns the request as matchers and templates read it, as JSON: as JSON writes it, with what the server's parsers
// read of each message in place of the message.
func (c *Call) view() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := c.server.resolver

	if !c.isClientStream() {
		return c.parsedAt(0, c.message).encode(r)
	}

	return jsonArray(len(c.messages), func(i int) ([]byte, error) { return c.parsedAt(i, c.messages[i]).encode(r) })
}

// received returns the request messages of the call so far: the one of a unary or server streaming call. c.mu is held.
func (c *Call) received() []proto.Message {
	if c.message != nil {
		return []proto.Message{c.message}
	}

	return slices.Clip(c.messages)
}

// parsedAt returns what the server's parsers read of msg, the i-th request message, or msg itself, which they didn't
// read. c.mu is held.
func (c *Call) parsedAt(i int, msg proto.Message) parsedMessage {
	if i < len(c.parsed) {
		return c.parsed[i]
	}

	return parsedMessage{value: msg, json: nil}
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
