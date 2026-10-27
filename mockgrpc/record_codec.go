package mockgrpc

import (
	json "encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.RecordCodec[*recordedCall, RecordedRequest, RecordedResponse] = (*recordCodec)(nil)

// MatcherSpec is a matcher as mock files write it, such as ["equal", "a"] or ["not", "present"].
type MatcherSpec []any

// RecordedRequest is what a recording matches: the request section of its mock file. A message is written as protojson
// writes it, with the field names of the .proto file, decoded as JSON, such as a map[string]any.
type RecordedRequest struct {
	// Method is the full method, such as /pkg.Service/Method.
	Method string `json:"method"`

	// Header matches the metadata RecordConfig.RequestHeaders names.
	Header map[string]MatcherSpec `json:"header,omitempty"`

	// Message is the request of a unary or server streaming call.
	Message any `json:"message,omitzero"`

	// Messages are the requests of a client streaming call, in order. A bidi call matches no message.
	Messages []any `json:"messages,omitzero"`
}

// RecordedResponse is the answer a recording replies with: the response section of its mock file.
type RecordedResponse struct {
	Header  map[string][]string `json:"header,omitempty"`
	Trailer map[string][]string `json:"trailer,omitempty"`

	// Message is the answer of a unary or client streaming call that ended with OK.
	Message any `json:"message,omitzero"`

	// Messages are the answers of a server or bidi streaming call, in order.
	Messages []any `json:"messages,omitempty"`

	// Status is the status the call ended with, unless OK.
	Status *RecordedStatus `json:"status,omitzero"`
}

// RecordedStatus is the status a recorded call ended with.
type RecordedStatus struct {
	// Code is the name of the code, such as NOT_FOUND.
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`

	// Details are the details of the status, each an Any as protojson writes it, with its "@type".
	Details []any `json:"details,omitempty"`
}

// recordedMockFile is the mock file of a recording.
type recordedMockFile struct {
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	Request  RecordedRequest   `json:"request"`
	Response *RecordedResponse `json:"response,omitzero"`
	Sequence *recordedSequence `json:"response_sequence,omitzero"`
}

type recordedSequence struct {
	Responses  []RecordedResponse `json:"responses"`
	AfterEnded RecordedResponse   `json:"after_ended"`
}

// recordedCall is a call the server captured for a recording: the request as the client sent it, and the answer as
// the client got it.
type recordedCall struct {
	method   string
	desc     protoreflect.MethodDescriptor
	header   metadata.MD
	requests []proto.Message

	resHeader metadata.MD
	trailer   metadata.MD
	messages  []proto.Message
	status    *status.Status
}

// size is about how many bytes the call holds in memory.
func (x *recordedCall) size() int {
	n := mdSize(x.header) + mdSize(x.resHeader) + mdSize(x.trailer)
	for _, msg := range slices.Concat(x.requests, x.messages) {
		n += proto.Size(msg)
	}

	return n
}

// recordCodec reads the calls the server captured as mocks.
type recordCodec struct {
	resolver *resolver
	headers  []string // the metadata keys recordings match, in lower case
	logger   *zerolog.Logger
}

// Name names the mock of x: its full method, without the leading "/", such as pkg.Service/Method.
func (c *recordCodec) Name(x *recordedCall) string {
	return strings.TrimPrefix(x.method, "/")
}

// Request writes what the recording of x matches: its method, the metadata the recording names, and its messages.
func (c *recordCodec) Request(x *recordedCall, _ *vlatte.RecordFiles) (RecordedRequest, error) {
	q := RecordedRequest{Method: x.method}

	for _, key := range c.headers {
		values := x.header.Get(key)

		switch {
		case len(values) == 0:
			q.Header = setSpec(q.Header, key, MatcherSpec{"not", "present"})
		case len(values) > 1:
			c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: the recording matches any %s: it has %d values", key, len(values))
		case !utf8.ValidString(values[0]):
			c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: the recording matches any %s: its value isn't UTF-8 text", key)
		default:
			q.Header = setSpec(q.Header, key, MatcherSpec{"equal", values[0]})
		}
	}

	switch typeOf(x.desc) {
	case _unary, _serverStream:
		msg, err := c.message(x.requests[0])
		if err != nil {
			return RecordedRequest{}, err
		}

		q.Message = msg
	case _clientStream:
		q.Messages = make([]any, 0, len(x.requests))

		for i, m := range x.requests {
			msg, err := c.message(m)
			if err != nil {
				return RecordedRequest{}, fmt.Errorf("message %d: %w", i, err)
			}

			q.Messages = append(q.Messages, msg)
		}
	case _bidiStream:
	}

	return q, nil
}

// Response writes what x answered: its header, messages, trailer and status, as the client got them.
func (c *recordCodec) Response(x *recordedCall, _ *vlatte.RecordFiles) (RecordedResponse, error) {
	s := RecordedResponse{Header: c.metadata(x, x.resHeader), Trailer: c.metadata(x, x.trailer)}

	for i, m := range x.messages {
		msg, err := c.message(m)
		if err != nil {
			return RecordedResponse{}, fmt.Errorf("answer %d: %w", i, err)
		}

		if x.desc.IsStreamingServer() {
			s.Messages = append(s.Messages, msg)
		} else {
			s.Message = msg
		}
	}

	if x.status.Code() == codes.OK {
		return s, nil
	}

	s.Status = &RecordedStatus{Code: codeName(x.status.Code()), Message: x.status.Message()}

	for i, d := range x.status.Proto().GetDetails() {
		detail, err := c.message(d)
		if err != nil {
			return RecordedResponse{}, fmt.Errorf("status detail %d: %w", i, err)
		}

		s.Status.Details = append(s.Status.Details, detail)
	}

	return s, nil
}

// Mock writes the mock file of m: its response, or a sequence of its responses whose last one repeats.
func (c *recordCodec) Mock(m *RecordedMock) any {
	doc := recordedMockFile{ID: m.ID, Name: m.Name, Request: m.Request}

	if len(m.Responses) == 1 {
		doc.Response = &m.Responses[0]
	} else {
		doc.Sequence = &recordedSequence{Responses: m.Responses, AfterEnded: m.Responses[len(m.Responses)-1]}
	}

	return doc
}

// message returns msg as a mock file holds it: as protojson writes it, decoded as JSON, so equal messages are written
// alike. A message protojson writes as a JSON string, such as a Timestamp, is its JSON text, quotes included: a mock
// file reads a string message as JSON text.
func (c *recordCodec) message(msg proto.Message) (any, error) {
	b, err := c.resolver.encode(msg)
	if err != nil {
		return nil, err
	}

	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}

	if _, ok := v.(string); ok {
		return string(b), nil
	}

	return v, nil
}

// metadata returns the metadata of x's answer a recording keeps: what the server doesn't set of its own, such as the
// debug headers. A value that isn't UTF-8 text is left out.
func (c *recordCodec) metadata(x *recordedCall, md metadata.MD) map[string][]string {
	var out map[string][]string

	for k, values := range md {
		if reservedKey(k) || slices.Contains(_debugKeys, k) {
			continue
		}

		for _, v := range values {
			if !utf8.ValidString(v) {
				c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: a value of %s isn't UTF-8 text: it isn't recorded", k)
				continue
			}

			if out == nil {
				out = make(map[string][]string, len(md))
			}

			out[k] = append(out[k], v)
		}
	}

	return out
}

// setSpec sets the matcher of key in specs, which it makes when nil.
func setSpec(specs map[string]MatcherSpec, key string, spec MatcherSpec) map[string]MatcherSpec {
	if specs == nil {
		specs = make(map[string]MatcherSpec)
	}

	specs[key] = spec

	return specs
}

// mdSize is about how many bytes md holds.
func mdSize(md metadata.MD) int {
	n := 0
	for k, values := range md {
		n += len(k)
		for _, v := range values {
			n += len(v)
		}
	}

	return n
}
