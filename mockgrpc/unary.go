package mockgrpc

import (
	"context"
	json "encoding/json/v2"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ UnaryReplierFactory = (*UnaryReply)(nil)
	_ UnaryReplier        = (*unaryReplier)(nil)
)

type UnaryValueSelectorIn struct {
	RequestMessage any
	Info           *grpc.UnaryServerInfo
}

type UnaryRequestValues struct {
	RequestMessage any
	Info           *grpc.UnaryServerInfo
	RawBody        any
	App            *Server
}

type UnaryResponse struct {
	Header  metadata.MD
	Trailer metadata.MD
	Message any
	Status  *status.Status
}

func (in *interceptors) UnaryInterceptor(
	ctx context.Context,
	reqMsg any,
	info *grpc.UnaryServerInfo,
	_ grpc.UnaryHandler,
) (any, error) {
	// Proto3 treats zero values as unset, so leave them out as protojson does.
	b, err := json.Marshal(reqMsg, json.OmitZeroStructFields(true), json.Deterministic(true))
	if err != nil {
		return nil, err
	}

	em, err := in.app.storage.List(ctx)
	if err != nil {
		return nil, interceptError("http: error finding eligible mocks for request. %w", err)
	}

	rawBody := string(b)
	sub := vlatte.NewExplanation()
	mocks := vlatte.Mocks[*Mock, *UnaryValueSelectorIn](em)
	mock, pass, err := mocks.Find(ctx, sub, &UnaryValueSelectorIn{rawBody, info}, func(m *Mock) vlatte.Evaluators[*Mock, *UnaryValueSelectorIn] { return m.unaryExpectations })
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Errorf("unary: error finding mock for request: %w", err).Error())
	}

	if !pass {
		return nil, status.Error(codes.NotFound, "unary: request was not matched with any mock")
	}

	replier, ok := mock.reply.(vlatte.Replier[*UnaryRequestValues, any])
	if !ok || replier == nil {
		return nil, interceptError("unary: mock %s reply must implement Replier[*UnaryRequestValues, any]: got %T", mock.getRef(), mock.reply)
	}

	rv := &UnaryRequestValues{reqMsg, info, rawBody, in.app}
	res, err := replier.Reply(ctx, rv)
	if err != nil {
		return nil, err
	}

	mock.Inc()

	return res, nil
}

// UnaryReply describes the reply of a unary mock: its header, trailer, and message or status. It is a
// UnaryReplierFactory: each mock built with it gets a replier of its own, so changing it after Mock changes only the
// mocks built afterwards. It keeps the message it is given without copying it: don't change it afterwards.
type UnaryReply struct {
	response *UnaryResponse
}

func Unary() *UnaryReply {
	return &UnaryReply{response: &UnaryResponse{
		Header:  make(metadata.MD),
		Trailer: make(metadata.MD),
	}}
}

func (u *UnaryReply) Status(st *status.Status) *UnaryReply {
	u.response.Status = st
	return u
}

func (u *UnaryReply) Header(k string, v ...string) *UnaryReply {
	u.response.Header.Append(k, v...)
	return u
}

func (u *UnaryReply) Trailer(k string, v ...string) *UnaryReply {
	u.response.Trailer.Append(k, v...)
	return u
}

func (u *UnaryReply) Message(msg any) *UnaryReply {
	u.response.Message = msg
	return u
}

// NewReplier implements UnaryReplierFactory. The replier has a header and trailer of its own.
func (u *UnaryReply) NewReplier(*Server) (UnaryReplier, error) {
	return &unaryReplier{response: UnaryResponse{
		Header:  u.response.Header.Copy(),
		Trailer: u.response.Trailer.Copy(),
		Message: u.response.Message,
		Status:  u.response.Status,
	}}, nil
}

// unaryReplier serves the calls of one unary mock. UnaryReply.NewReplier makes it, and calls only read it: gRPC sends
// the header and trailer without writing into them.
type unaryReplier struct {
	response UnaryResponse
}

// Reply implements UnaryReplier.
func (r *unaryReplier) Reply(ctx context.Context, _ *UnaryRequestValues) (any, error) {
	err := grpc.SendHeader(ctx, r.response.Header)
	if err != nil {
		return nil, err
	}

	err = grpc.SetTrailer(ctx, r.response.Trailer)
	if err != nil {
		return nil, err
	}

	if r.response.Status != nil {
		return nil, r.response.Status.Err()
	}

	return r.response.Message, nil
}
