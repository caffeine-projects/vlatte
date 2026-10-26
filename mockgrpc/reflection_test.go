package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	v1 "google.golang.org/grpc/reflection/grpc_reflection_v1"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestReflection_ListsTheServicesTheServerServes(t *testing.T) {
	// Tools such as grpcurl list a server's services through reflection: a service nobody registered is served as
	// soon as a mock names it, so reflection lists it too, and the services of the server's descriptors.
	m := MustNew(Setup().Silent().Reflection().Descriptors(writeDescriptorSet(t, "dyn")))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("protobuf.TestService/Greetings").Reply(Message(&pb.HiRes{})))

	stream, err := v1.NewServerReflectionClient(m.MustDial()).ServerReflectionInfo(t.Context())
	require.NoError(t, err)

	require.NoError(t, stream.Send(&v1.ServerReflectionRequest{
		MessageRequest: &v1.ServerReflectionRequest_ListServices{},
	}))

	res, err := stream.Recv()
	require.NoError(t, err)

	services := res.GetListServicesResponse().GetService()
	names := make([]string, 0, len(services))

	for _, svc := range services {
		names = append(names, svc.GetName())
	}

	require.ElementsMatch(t, []string{
		"grpc.reflection.v1.ServerReflection",
		"grpc.reflection.v1alpha.ServerReflection",
		"dyn.TestService",
		"dyn.OtherService",
		"protobuf.TestService",
	}, names)

	// Its files describe the messages.
	require.NoError(t, stream.Send(&v1.ServerReflectionRequest{
		MessageRequest: &v1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: "dyn.HiReq"},
	}))

	res, err = stream.Recv()
	require.NoError(t, err)
	require.NotEmpty(t, res.GetFileDescriptorResponse().GetFileDescriptorProto())
}

func TestReflection_ServesTheV1AlphaServiceToo(t *testing.T) {
	// Older tools ask grpc.reflection.v1alpha: the mocks answered it, with no mock matched. Its messages are v1's.
	m := MustNew(Setup().Silent().Reflection())
	m.MustStart()
	t.Cleanup(m.Close)

	stream, err := m.MustDial().NewStream(t.Context(), &grpc.StreamDesc{ClientStreams: true, ServerStreams: true},
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&v1.ServerReflectionRequest{MessageRequest: &v1.ServerReflectionRequest_ListServices{}}))

	var res v1.ServerReflectionResponse
	require.NoError(t, stream.RecvMsg(&res))
	require.NotEmpty(t, res.GetListServicesResponse().GetService())
}

func TestReflection_ServesTheFilesOfTheServersDescriptorsByName(t *testing.T) {
	// grpcurl asks for the files a service's file imports by their names: those of the server's descriptors resolve
	// as those of generated code do, and an unknown one is an error, not an empty file.
	m := MustNew(Setup().Silent().Reflection().Descriptors(writeDescriptorSet(t, "dyn")))
	m.MustStart()
	t.Cleanup(m.Close)

	stream, err := v1.NewServerReflectionClient(m.MustDial()).ServerReflectionInfo(t.Context())
	require.NoError(t, err)

	for _, name := range []string{"dyn.proto", "google/protobuf/timestamp.proto"} {
		require.NoError(t, stream.Send(&v1.ServerReflectionRequest{
			MessageRequest: &v1.ServerReflectionRequest_FileByFilename{FileByFilename: name},
		}))

		res, err := stream.Recv()
		require.NoError(t, err)
		require.NotEmpty(t, res.GetFileDescriptorResponse().GetFileDescriptorProto(), name)
	}

	for _, req := range []*v1.ServerReflectionRequest{
		{MessageRequest: &v1.ServerReflectionRequest_FileByFilename{FileByFilename: "nope.proto"}},
		{MessageRequest: &v1.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: "dyn.Nope"}},
	} {
		require.NoError(t, stream.Send(req))

		res, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, int32(codes.NotFound), res.GetErrorResponse().GetErrorCode(), "%v", req)
	}
}
