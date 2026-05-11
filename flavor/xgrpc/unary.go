package xgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
)

type UnaryValueSelector func(ctx context.Context, in *UnaryValueSelectorIn) any

type UnaryValueSelectorIn struct {
	RequestMessage any
	Info           *grpc.UnaryServerInfo
}

type UnaryRequestValues struct {
	RequestMessage any
	Info           *grpc.UnaryServerInfo
	RawBody        any
	App            *GRPCMockApp
}

type UnaryResponse struct {
	Header  metadata.MD
	Trailer metadata.MD
	Message any
	Status  *status.Status
}

func (in *Interceptors) UnaryInterceptor(
	ctx context.Context,
	reqMsg interface{},
	info *grpc.UnaryServerInfo,
	_ grpc.UnaryHandler,
) (any, error) {
	b, err := json.Marshal(reqMsg)
	if err != nil {
		return nil, err
	}

	em, err := in.app.storage.List(ctx)
	if err != nil {
		return nil, interceptError("http: error finding eligible mocks for request. %w", err)
	}

	rawBody := string(b)
	sub := explain.New()
	mocks := vlatte.Mocks[*GRPCMock, *UnaryValueSelectorIn](em)
	mock, pass, err := mocks.Find(ctx, sub, &UnaryValueSelectorIn{rawBody, info}, func(m *GRPCMock) vlatte.Evaluators[*GRPCMock, *UnaryValueSelectorIn] { return m.unaryExpectations })
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Errorf("unary: error finding mock for request: %w", err).Error())
	}

	if !pass {
		return nil, interceptError("unary: request was not matched with any mock")
	}

	replier, ok := mock.Reply.(vlatte.Replier[*UnaryRequestValues, any])
	if !ok || replier == nil {
		return nil, interceptError("unary: mock %s reply must implement Replier[*UnaryRequestValues, any]: got %T", mock.getRef(), mock.Reply)
	}

	rv := &UnaryRequestValues{reqMsg, info, rawBody, in.app}
	res, err := replier.Reply(ctx, rv)
	if err != nil {
		return nil, err
	}

	mock.Inc()

	return res, nil
}

var _ vlatte.Replier[*UnaryRequestValues, any] = (*BuiltInUnaryReply)(nil)

type BuiltInUnaryReply struct {
	response *UnaryResponse
}

func Unary() *BuiltInUnaryReply {
	return &BuiltInUnaryReply{response: &UnaryResponse{
		Header:  make(metadata.MD),
		Trailer: make(metadata.MD),
	}}
}

func (u *BuiltInUnaryReply) Status(st *status.Status) *BuiltInUnaryReply {
	u.response.Status = st
	return u
}

func (u *BuiltInUnaryReply) Header(k string, v ...string) *BuiltInUnaryReply {
	u.response.Header.Append(k, v...)
	return u
}

func (u *BuiltInUnaryReply) Trailer(k string, v ...string) *BuiltInUnaryReply {
	u.response.Trailer.Append(k, v...)
	return u
}

func (u *BuiltInUnaryReply) Message(msg any) *BuiltInUnaryReply {
	u.response.Message = msg
	return u
}

// Reply implements vlatte.Replier[*UnaryRequestValues, any].
func (u *BuiltInUnaryReply) Reply(ctx context.Context, _ *UnaryRequestValues) (any, error) {
	err := grpc.SendHeader(ctx, u.response.Header)
	if err != nil {
		return nil, err
	}

	err = grpc.SetTrailer(ctx, u.response.Trailer)
	if err != nil {
		return nil, err
	}

	if u.response.Status != nil {
		return nil, u.response.Status.Err()
	}

	return u.response.Message, nil
}
