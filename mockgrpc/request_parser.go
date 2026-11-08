package mockgrpc

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
)

var _ error = (*parseError)(nil)

// ErrParsing is wrapped by the error of a request message a [RequestParser] failed to parse, such as the one
// [Call.Recv] returns.
var ErrParsing = errors.New("request_parser: a parser failed to read the request")

// RequestParser reads a request message as what it holds, such as an inner message one of its fields carries
// encrypted. Register parsers with [ConfigBuilder.RequestParsers]. For each message the client sends, the server asks
// its parsers in order, and the first whose CanParse reports true parses it. A message no parser can parse is read as
// itself, as a server without parsers reads every message.
//
// What a parser read is what [Builder.Field], [Builder.Body], the rules of [BidiReply.On] and the .Request.Message of
// templates read, and what [Call.Parsed] returns: request.fields, request.body and bidi.on.when of mock files read it
// too. The rest reads the message as the client sent it: [Builder.Message], [Builder.Messages] and [Builder.JSON],
// [Call.Message], [Call.Messages] and [Call.JSON], proxies, [Echo], the journal, recordings, and
// [github.com/caffeine-projects/vlatte/verify.Requests.Body]. The vlatte command has no parsers: its mock files read
// every message as the client sent it.
//
// The server parses the request of a unary or streaming server call, and each message of a streaming client, once it
// received them, before it matches the call. A bidi call is matched before any message: [Call.Recv] parses each
// message as it receives it, before its Bidi rule is chosen, while proxies and Echo forward bidi messages unparsed.
// Each message is parsed once per call, however many mocks, matchers and templates read it.
//
// An error of the parser that parses a message fails the call, and no other parser is tried. An error that carries a
// gRPC status, such as status.Error(codes.InvalidArgument, "bad ciphertext") makes, is the answer the client gets, as a
// real server rejecting the message would answer. Any other error, or a panic, answers Internal, with the vlatte-error
// trailer "match": no mock answers. A bidi message is parsed once its mock matched: a status is the mock's answer, which
// counts the call, and any other error fails its reply, with the trailer "reply". The client reads the error's text:
// keep keys and decrypted text out of it.
//
// One parser serves every call of the server at once, and also parses the calls of journal entries again, as
// [github.com/caffeine-projects/vlatte/verify.Requests] checks them and the admin API searches them, once their method
// and header pass, so it must be safe for concurrent use, and read a message the same way each time. A journal entry keeps the request as JSON: the message parsed again has no unknown
// fields.
type RequestParser interface {
	// CanParse reports whether Parse reads msg, a request message of call. msg and call are the server's: don't change
	// them.
	CanParse(call *Call, msg proto.Message) bool

	// Parse returns what msg, a request message of call, holds: a proto.Message, read as protojson writes it, with the
	// field names of the .proto file, or any value encoding/json/v2 writes, such as a map or a jsontext.Value, read as
	// that JSON. A nil value reads as JSON null. A message nested in another value isn't read as protojson writes it.
	//
	// Read the metadata of the call with call.Header, not from ctx: the call of a journal entry has only its header.
	// Parse should return once ctx ends. Don't call call.Send or call.Recv, and don't change msg, nor the value once
	// Parse returned it: the server keeps it for the call.
	Parse(ctx context.Context, call *Call, msg proto.Message) (any, error)
}

// parsedMessage is what the server's parsers read of a request message: value, and value as JSON, as matchers and
// templates read it. A message no parser read is its own value, with no JSON: it is written as JSON when read.
type parsedMessage struct {
	value any
	json  []byte
}

// encode returns p as JSON, as matchers and templates read it: what a parser read, or the message as r writes it.
func (p parsedMessage) encode(r *resolver) ([]byte, error) {
	if p.json != nil {
		return p.json, nil
	}

	msg, _ := p.value.(proto.Message)

	return r.encode(msg)
}

// parseError is the error of a request message a parser failed to parse: the index-th of the call's request.
type parseError struct {
	index  int
	parser RequestParser
	err    error
}

func (e *parseError) Error() string {
	return fmt.Sprintf("%v: message %d: %T: %v", ErrParsing, e.index, e.parser, e.err)
}

func (e *parseError) Unwrap() []error {
	return []error{ErrParsing, e.err}
}

// GRPCStatus returns the status of the parser's error, which the call answers with, or nil when it has none.
func (e *parseError) GRPCStatus() *status.Status {
	if s, ok := status.FromError(e.err); ok && s.Code() != codes.OK {
		return s
	}

	return nil
}

// parse returns what the server's parsers read of msg, the index-th request message of call: the value of the first
// parser that can parse it, or msg itself, with no JSON, when none can. An error is a parseError.
func (app *Server) parse(ctx context.Context, call *Call, msg proto.Message, index int) (parsedMessage, error) {
	for _, p := range app.config.RequestParsers {
		can, err := try(func() (bool, error) { return p.CanParse(call, msg), nil })
		if err != nil {
			return parsedMessage{value: nil, json: []byte("null")}, &parseError{index: index, parser: p, err: err}
		}

		if !can {
			continue
		}

		// The value is written as JSON within the parse: a value whose MarshalJSON panics fails it, as a panic of Parse.
		var v any

		b, err := try(func() ([]byte, error) {
			var err error
			if v, err = p.Parse(ctx, call, msg); err != nil {
				return nil, err
			}

			return app.resolver.encodeValue(v)
		})
		if err == nil {
			return parsedMessage{value: v, json: b}, nil
		}

		return parsedMessage{value: nil, json: []byte("null")}, &parseError{index: index, parser: p, err: err}
	}

	return parsedMessage{value: msg, json: nil}, nil
}

// parseRequest has the server's parsers read the request of call, which it received whole: the message of a unary or
// streaming server call, each message of a streaming client, in order, until one fails. The JSON matchers read is then
// what they read. A server without parsers reads none.
func (app *Server) parseRequest(ctx context.Context, call *Call) error {
	if len(app.config.RequestParsers) == 0 {
		return nil
	}

	msgs := call.Messages()
	parsed := make([]parsedMessage, 0, len(msgs))

	var err error
	for i, msg := range msgs {
		if err = ctx.Err(); err != nil {
			break
		}

		var p parsedMessage
		p, err = app.parse(ctx, call, msg, i)
		parsed = append(parsed, p)

		if err != nil {
			break
		}
	}

	call.mu.Lock()
	call.parsed = parsed
	call.mu.Unlock()

	if err != nil {
		return err
	}

	// The view writes: each message no parser read was written as JSON before, as the call was served or journaled,
	// and the others are what their parsers read, written as they read them.
	call.json, _ = call.view()

	return nil
}

// parseFailed answers call, whose request the server's parsers failed to read with err: with the status of the
// parser's error, when it carries one, else as a failure to match. A call whose context ended ends with its code.
func (app *Server) parseFailed(ctx context.Context, call *Call, err error) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}

	if hasStatus(err) {
		callOf(ctx).failed(err)
		return status.Convert(err).Err()
	}

	app.logger.Warn().Err(err).Str("method", call.method).Msg("grpc: parsing the request")

	return fail(ctx, vlatte.FailureMatch, codes.Internal, "%s: %s: %v", typeOf(call.desc), call.method, err)
}

// encodeValue returns v, a value a parser read, as JSON, as matchers and templates read it: a protobuf message as
// encode writes it, and any other value as encoding/json/v2 writes it, with map keys in order. A nil message is null.
func (r *resolver) encodeValue(v any) ([]byte, error) {
	msg, ok := v.(proto.Message)
	switch {
	case ok && !msg.ProtoReflect().IsValid():
		return []byte("null"), nil
	case ok:
		return r.encode(msg)
	}

	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("writing the parsed value as JSON: %w", err)
	}

	return b, nil
}

// try returns what fn returns, or the value of its panic as an error.
func try[T any](fn func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	return fn()
}
