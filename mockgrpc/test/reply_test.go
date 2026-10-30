package test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte/mockgrpc"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

var _ grpc.ServerTransportStream = metadataFailure{}

type metadataFailure struct {
	header  error
	trailer error
}

func (metadataFailure) Method() string                 { return "" }
func (metadataFailure) SetHeader(metadata.MD) error    { return nil }
func (f metadataFailure) SendHeader(metadata.MD) error { return f.header }
func (f metadataFailure) SetTrailer(metadata.MD) error { return f.trailer }

func TestReplies_MetadataFailuresReachTheCaller(t *testing.T) {
	errMetadata := status.Error(codes.Unavailable, "test: metadata unavailable")
	tests := []struct {
		name      string
		method    string
		reply     mockgrpc.Reply
		transport metadataFailure
	}{
		{name: "response header", method: pb.TestService_Greetings_FullMethodName, reply: mockgrpc.Message(&pb.HiRes{}), transport: metadataFailure{header: errMetadata}},
		{name: "response trailer", method: pb.TestService_Greetings_FullMethodName, reply: mockgrpc.Message(&pb.HiRes{}).Trailer("x-result", "tea"), transport: metadataFailure{trailer: errMetadata}},
		{name: "bidi header", method: pb.TestService_Chat_FullMethodName, reply: mockgrpc.Bidi(), transport: metadataFailure{header: errMetadata}},
		{name: "bidi trailer", method: pb.TestService_Chat_FullMethodName, reply: mockgrpc.Bidi().Trailer("x-result", "tea"), transport: metadataFailure{trailer: errMetadata}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mockgrpc.MustNew(mockgrpc.Setup().Silent().InMemory().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
			m.MustStart()
			t.Cleanup(m.Close)
			got := make(chan error, 1)
			m.MustMock(mockgrpc.Method(tt.method).Reply(mockgrpc.Func(func(ctx context.Context, call *mockgrpc.Call) error {
				replier, err := tt.reply.NewReplier(m, []protoreflect.MethodDescriptor{call.Descriptor()})
				if err == nil {
					err = replier.Reply(grpc.NewContextWithServerTransportStream(ctx, tt.transport), call)
				}
				got <- err
				return err
			})))
			err := m.MustDial().Invoke(t.Context(), tt.method, &pb.HiReq{}, &pb.HiRes{})
			require.Equal(t, codes.Unavailable, status.Code(err))
			select {
			case err := <-got:
				require.ErrorIs(t, err, errMetadata)
			default:
				t.Fatal("the call ended before its reply ran")
			}
		})
	}
}
