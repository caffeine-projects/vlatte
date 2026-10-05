package mockgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
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
	b, err := json.Marshal(reqMsg)
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

var _ vlatte.Replier[*UnaryRequestValues, any] = (*UnaryReply)(nil)

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

// Reply implements vlatte.Replier[*UnaryRequestValues, any].
func (u *UnaryReply) Reply(ctx context.Context, _ *UnaryRequestValues) (any, error) {
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
