package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
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
