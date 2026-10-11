package mockgrpc

import (
	"bufio"
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"reflect"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ StreamReplierFactory = (*StreamReply[proto.Message])(nil)
	_ StreamReplier        = (*streamReplier[proto.Message])(nil)
)

type StreamValueSelectorIn struct {
	RequestMessage any
	Info           *grpc.StreamServerInfo
}

type StreamRequestValues struct {
	RequestMessage any
	ServerStream   grpc.ServerStream
	Info           *grpc.StreamServerInfo
	App            *Server
}

type StreamType int

const (
	StreamTypeJSON StreamType = iota
	StreamTypeText
)

type StreamResponse[T proto.Message] struct {
	Header     metadata.MD
	Trailer    metadata.MD
	MsgType    T
	Stream     any // a slice of messages, from Messages or AnyMessages, or the bytes of Text or JSON, a message per line
	StreamType StreamType
}

// StreamReply describes the reply of a server streaming mock: its header, trailer and messages. It is a
// StreamReplierFactory: each mock built with it gets a replier of its own, so changing it after Mock changes only the
// mocks built afterwards. It keeps the message slices it is given without copying them: don't change them afterwards.
type StreamReply[T proto.Message] struct {
	response *StreamResponse[T]
	err      error // from reading the reader given to Text or JSON; NewReplier returns it
}

func (r *StreamReply[T]) Messages(arr []proto.Message) *StreamReply[T] {
	r.response.Stream = arr
	r.err = nil
	return r
}

func (r *StreamReply[T]) AnyMessages(arr []any) *StreamReply[T] {
	r.response.Stream = arr
	r.err = nil
	return r
}

// Text streams the messages in reader, one per line, in the protobuf text format. The reader is read now, so each
// call of each mock built with the reply streams all of them.
func (r *StreamReply[T]) Text(reader io.Reader) *StreamReply[T] {
	return r.read(reader, StreamTypeText)
}

// JSON streams the messages in reader, one per line, in the protobuf JSON format. The reader is read now, so each call
// of each mock built with the reply streams all of them.
func (r *StreamReply[T]) JSON(reader io.Reader) *StreamReply[T] {
	return r.read(reader, StreamTypeJSON)
}

// NewReplier implements StreamReplierFactory. The replier has a header and trailer of its own.
func (r *StreamReply[T]) NewReplier(*Server) (StreamReplier, error) {
	if r.err != nil {
		return nil, fmt.Errorf("stream: reading the messages: %w", r.err)
	}

	return &streamReplier[T]{response: StreamResponse[T]{
		Header:     r.response.Header.Copy(),
		Trailer:    r.response.Trailer.Copy(),
		MsgType:    r.response.MsgType,
		Stream:     r.response.Stream,
		StreamType: r.response.StreamType,
	}}, nil
}

// read reads all of reader, and keeps the error for NewReplier.
func (r *StreamReply[T]) read(reader io.Reader, t StreamType) *StreamReply[T] {
	b, err := io.ReadAll(reader)
	r.err = err
	r.response.Stream = b
	r.response.StreamType = t

	return r
}

// streamReplier serves the calls of one server streaming mock. StreamReply.NewReplier makes it, and calls only read it:
// gRPC sends the header and trailer without writing into them.
type streamReplier[T proto.Message] struct {
	response StreamResponse[T]
}

// Reply implements StreamReplier.
func (r *streamReplier[T]) Reply(ctx context.Context, values *StreamRequestValues) (any, error) {
	err := r.build(ctx, values)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *streamReplier[T]) build(ctx context.Context, values *StreamRequestValues) error {
	err := grpc.SendHeader(ctx, r.response.Header)
	if err != nil {
		return err
	}

	err = grpc.SetTrailer(ctx, r.response.Trailer)
	if err != nil {
		return err
	}

	switch s := r.response.Stream.(type) {
	case []byte: // the messages of Text or JSON, one per line
		scan := bufio.NewScanner(bytes.NewReader(s))
		msg := r.response.MsgType.ProtoReflect().New().Interface() // MsgType is the zero T, a nil message

		for scan.Scan() {
			err := r.decode(scan.Bytes(), msg)
			if err != nil {
				return err
			}

			err = values.ServerStream.SendMsg(msg)
			if err != nil {
				return err
			}
		}

	default:
		t := reflect.TypeOf(s)
		switch t.Kind() {
		case reflect.Array, reflect.Slice:
			v := reflect.ValueOf(s)
			for i := 0; i < v.Len(); i++ {
				err := values.ServerStream.SendMsg(v.Index(i).Interface())
				if err != nil {
					return err
				}
			}

		default:
			return status.Errorf(
				codes.Internal,
				"stream: reply stream type %T is not allowed. expected: io.Reader | array/slice",
				s,
			)
		}
	}

	return nil
}

func (r *streamReplier[T]) decode(b []byte, m proto.Message) error {
	switch r.response.StreamType {
	case StreamTypeText:
		return prototext.Unmarshal(b, m)
	case StreamTypeJSON:
		return protojson.Unmarshal(b, m)
	}

	return fmt.Errorf("stream: unexpected stream type %d", r.response.StreamType)
}

// receiveRequest reads the request message of a server streaming call.
// Stream interceptors do not receive it: their first argument is the service implementation.
func receiveRequest(stream grpc.ServerStream, info *grpc.StreamServerInfo) (proto.Message, error) {
	svc, method, ok := strings.Cut(strings.TrimPrefix(info.FullMethod, "/"), "/")
	if !ok {
		return nil, fmt.Errorf("unexpected method name %q", info.FullMethod)
	}

	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(svc))
	if err != nil {
		return nil, err
	}

	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a service", svc)
	}

	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("method %s not found in service %s", method, svc)
	}

	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
	if err != nil {
		return nil, err
	}

	msg := mt.New().Interface()
	if err = stream.RecvMsg(msg); err != nil {
		return nil, err
	}

	return msg, nil
}

func ServerStream[T proto.Message]() *StreamReply[T] {
	return &StreamReply[T]{response: &StreamResponse[T]{
		Header:  make(metadata.MD),
		Trailer: make(metadata.MD),
	}}
}

func (in *interceptors) StreamInterceptor(
	_ any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	_ grpc.StreamHandler,
) error {
	ctx := stream.Context()

	reqMsg, err := receiveRequest(stream, info)
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "stream: reading the request message: %v", err)
	}

	// Proto3 treats zero values as unset, so leave them out as protojson does.
	b, err := json.Marshal(reqMsg, json.OmitZeroStructFields(true), json.Deterministic(true))
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "stream: encoding the request message: %v", err)
	}

	em, err := in.app.storage.List(ctx)
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "stream: listing mocks: %v", err)
	}

	rawBody := string(b)
	sub := vlatte.NewExplanation()
	mocks := vlatte.Mocks[*Mock, *StreamValueSelectorIn](ofKind(em, func(m *Mock) bool { return m.streamReply != nil }))
	mock, pass, err := mocks.Find(ctx, sub, &StreamValueSelectorIn{rawBody, info}, func(m *Mock) vlatte.Evaluators[*Mock, *StreamValueSelectorIn] { return m.streamExpectations })
	if err != nil {
		return fail(ctx, vlatte.FailureMatch, codes.Internal, "stream: finding a mock: %v", err)
	}

	if !pass {
		return fail(ctx, vlatte.FailureNoMatch, codes.NotFound, "stream: request was not matched with any mock")
	}

	values := &StreamRequestValues{rawBody, stream, info, in.app}
	_, err = mock.streamReply.Reply(ctx, values)
	if err != nil {
		return replyError(ctx, "stream", mock, err)
	}

	mock.Inc()

	return nil
}
