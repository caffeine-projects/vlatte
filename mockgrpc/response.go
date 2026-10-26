package mockgrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	// Mock files name the google.rpc error details of a status by type: the program has them.
	_ "google.golang.org/genproto/googleapis/rpc/errdetails"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

var _ Reply = (*Response)(nil)

var (
	errDetailsWithoutStatus = errors.New("status details need a status other than OK: set one with Status")
	errMessageAndStatus     = errors.New("a call that answers one message ends with it or with a status, not both")
)

// Response describes the answer of a mock: its messages, header, trailer and the status it ends with. It is a Reply:
// each mock built with it gets a replier of its own, so changing it after Mock changes only the mocks built afterwards.
// It keeps the messages it is given without copying them: don't change them afterwards.
//
// A unary or client streaming method answers one message, or a status: the empty message of its output type when the
// Response has neither. A server or bidi streaming method answers each message in turn, then the status.
type Response struct {
	header     metadata.MD
	trailer    metadata.MD
	messages   []proto.Message
	json       []string
	templated  bool // json holds templates
	code       codes.Code
	message    string
	details    []proto.Message
	detailJSON []string // details as mock files give them: JSON of Any messages, read as the replier is made
	delay      vlatte.Delay
	// delayDuration is the delay of MessageDelay, which a description writes; MessageDelayFunc's can't be.
	delayDuration time.Duration
}

// Message returns a Response that answers msg, which must be of the type the method answers with.
func Message(msg proto.Message) *Response {
	return &Response{messages: []proto.Message{msg}}
}

// Messages returns a Response that answers msgs, in order: the messages of a server or bidi streaming answer.
func Messages(msgs ...proto.Message) *Response {
	return &Response{messages: msgs}
}

// JSON returns a Response that answers the messages texts hold, each as protojson reads it, in the type the method
// answers with: {"message": "hi"}. Build fails on text that doesn't hold such a message.
func JSON(texts ...string) *Response {
	return &Response{json: texts}
}

// Template returns a Response like JSON's, whose texts are templates of the server's template engine, rendered for each
// call with [TemplateData]: {"message": "hi {{ .Request.Message.name }}"}. A text that doesn't render a message of the
// type the method answers with fails the call.
func Template(texts ...string) *Response {
	return &Response{json: texts, templated: true}
}

// Status returns a Response that ends the call with code and msg, and no message.
func Status(code codes.Code, msg string) *Response {
	return &Response{code: code, message: msg}
}

// Header adds metadata the answer starts with, sent before its first message. The last value of a key the server sets
// itself, such as the debug headers, goes with them.
func (r *Response) Header(key string, values ...string) *Response {
	if r.header == nil {
		r.header = make(metadata.MD)
	}

	r.header.Append(key, values...)

	return r
}

// Trailer adds metadata the answer ends with.
func (r *Response) Trailer(key string, values ...string) *Response {
	if r.trailer == nil {
		r.trailer = make(metadata.MD)
	}

	r.trailer.Append(key, values...)

	return r
}

// Status sets the status the answer ends with, after its messages: a stream that fails after some messages. A unary
// or client streaming answer has a message or a status, not both.
func (r *Response) Status(code codes.Code, msg string) *Response {
	r.code, r.message = code, msg
	return r
}

// Details adds details to the status the answer ends with, such as the errdetails messages clients read with
// status.Details: &errdetails.BadRequest{...}. They need a status other than OK.
func (r *Response) Details(details ...proto.Message) *Response {
	r.details = append(r.details, details...)
	return r
}

// MessageDelay waits for d before each message of the answer: a stream that trickles in.
func (r *Response) MessageDelay(d time.Duration) *Response {
	r.delay, r.delayDuration = vlatte.Latency(d), d
	return r
}

// MessageDelayFunc sets a function that picks the delay before each message of the answer, such as [vlatte.Jitter].
func (r *Response) MessageDelayFunc(delay vlatte.Delay) *Response {
	r.delay, r.delayDuration = delay, 0
	return r
}

// NewReplier implements Reply. It checks the Response against each method: that its messages are of the type the
// method answers with, and that a method that answers one message gets one, or a status.
func (r *Response) NewReplier(app *Server, methods []protoreflect.MethodDescriptor) (Replier, error) {
	return r.replier(app, methods)
}

// replier makes the replier of NewReplier.
func (r *Response) replier(app *Server, methods []protoreflect.MethodDescriptor) (*responseReplier, error) {
	st, err := r.status(app.resolver)
	if err != nil {
		return nil, err
	}

	rep := &responseReplier{
		header:   r.header.Copy(),
		trailer:  r.trailer.Copy(),
		status:   st,
		delay:    r.delay,
		messages: make(map[string][]proto.Message, len(methods)),
	}

	if r.templated {
		for i, text := range r.json {
			t, err := app.templateEngine().Compile(fmt.Sprintf("message[%d]", i), text)
			if err != nil {
				return nil, fmt.Errorf("reply: template %d: %w", i, err)
			}

			rep.templates = append(rep.templates, t)
		}
	}

	for _, md := range methods {
		msgs, err := r.messagesOf(app.resolver, md)
		if err != nil {
			return nil, fmt.Errorf("reply: %s: %w", fullMethod(md), err)
		}

		n := len(msgs) + len(rep.templates)
		if !md.IsStreamingServer() {
			switch {
			case n > 1:
				return nil, fmt.Errorf("reply: %s answers one message, not %d", fullMethod(md), n)
			case n == 1 && st.Code() != codes.OK:
				return nil, fmt.Errorf("reply: %s: %w", fullMethod(md), errMessageAndStatus)
			}
		}

		rep.messages[fullMethod(md)] = msgs
	}

	return rep, nil
}

// status returns the status the Response ends with. A detail that is an Any, as mock files give them, goes as it is.
func (r *Response) status(res *resolver) (*status.Status, error) {
	if len(r.details) == 0 && len(r.detailJSON) == 0 {
		return status.New(r.code, r.message), nil
	}

	if r.code == codes.OK {
		return nil, fmt.Errorf("reply: %w", errDetailsWithoutStatus)
	}

	p := &spb.Status{Code: int32(r.code), Message: r.message}

	for i, d := range r.details {
		a, ok := d.(*anypb.Any)
		if !ok {
			var err error
			if a, err = anypb.New(d); err != nil {
				return nil, fmt.Errorf("reply: status detail %d: %w", i, err)
			}
		}

		p.Details = append(p.Details, a)
	}

	for i, text := range r.detailJSON {
		a := &anypb.Any{}
		if err := (protojson.UnmarshalOptions{Resolver: res}).Unmarshal([]byte(text), a); err != nil {
			return nil, fmt.Errorf("reply: status detail %d: %w", len(r.details)+i, err)
		}

		p.Details = append(p.Details, a)
	}

	return status.FromProto(p), nil
}

// messagesOf returns the messages of the Response for md: those it was given, which must be of md's output type, or
// those its JSON holds, read in that type. Templates render for each call.
func (r *Response) messagesOf(res *resolver, md protoreflect.MethodDescriptor) ([]proto.Message, error) {
	out := md.Output()

	if r.templated {
		return nil, nil
	}

	if r.json != nil {
		msgs := make([]proto.Message, len(r.json))
		for i, text := range r.json {
			msg, err := res.decode(out, []byte(text))
			if err != nil {
				return nil, fmt.Errorf("json %d: %w", i, err)
			}

			msgs[i] = msg
		}

		return msgs, nil
	}

	for i, msg := range r.messages {
		if got := msg.ProtoReflect().Descriptor().FullName(); got != out.FullName() {
			return nil, fmt.Errorf("message %d is a %s, the method answers with %s", i, got, out.FullName())
		}
	}

	return r.messages, nil
}

// responseReplier answers the calls of one mock with a Response. Calls only read it: gRPC sends the header and
// trailer without writing into them.
type responseReplier struct {
	header    metadata.MD
	trailer   metadata.MD
	messages  map[string][]proto.Message // by full method
	templates []vlatte.Template
	status    *status.Status
	delay     vlatte.Delay
}

// Reply implements Replier.
func (r *responseReplier) Reply(ctx context.Context, call *Call) error {
	// The header goes out first, so a client sees it before a delayed message, or with the status.
	if err := grpc.SendHeader(ctx, r.header); err != nil {
		return err
	}

	if err := grpc.SetTrailer(ctx, r.trailer); err != nil {
		return err
	}

	return r.send(ctx, call)
}

// send sends the messages of the answer, waiting before each for the delay, then returns the status it ends with.
func (r *responseReplier) send(ctx context.Context, call *Call) error {
	msgs, err := r.messagesFor(ctx, call)
	if err != nil {
		return err
	}

	for _, msg := range msgs {
		if r.delay != nil {
			if err := httpx.Wait(ctx, r.delay()); err != nil {
				return status.FromContextError(err).Err()
			}
		}

		if err := call.Send(msg); err != nil {
			return err
		}
	}

	return r.status.Err()
}

// messagesFor returns the messages that answer call: the ones of its method, or its templates rendered for it.
func (r *responseReplier) messagesFor(ctx context.Context, call *Call) ([]proto.Message, error) {
	if len(r.templates) == 0 {
		return r.messages[call.method], nil
	}

	data := call.server.templateData(ctx, call)

	msgs := make([]proto.Message, len(r.templates))
	for i, t := range r.templates {
		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("template %d: %w", i, err)
		}

		msg, err := call.server.resolver.decode(call.desc.Output(), buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("template %d: %w", i, err)
		}

		msgs[i] = msg
	}

	return msgs, nil
}
