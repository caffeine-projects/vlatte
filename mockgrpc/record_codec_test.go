package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// codecOf returns the record codec of a server, which matches headers.
func codecOf(t *testing.T, headers ...string) *recordCodec {
	t.Helper()

	m := recorder(t)

	return &recordCodec{resolver: m.resolver, headers: headers, logger: m.logger}
}

// recordedCallOf returns a call of method of the test service, with its requests.
func recordedCallOf(method string, requests ...proto.Message) *recordedCall {
	desc := pb.File_test_proto.Services().ByName("TestService").Methods().ByName(protoreflect.Name(method))

	return &recordedCall{method: "/protobuf.TestService/" + method, desc: desc, requests: requests, status: status.New(codes.OK, "")}
}

func TestRecordCodec_MatchesAnyHeaderItCantMatchExactly(t *testing.T) {
	c := codecOf(t, "many", "binary", "one")

	x := recordedCallOf("Greetings", &pb.HiReq{Message: "hi"})
	x.header = metadata.Pairs("many", "a", "many", "b", "binary", "\xff", "one", "a")

	q, err := c.Request(x, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]MatcherSpec{"one": {"equal", "a"}}, q.Header)
}

func TestRecordCodec_LeavesOutAnswerMetadataThatIsntText(t *testing.T) {
	c := codecOf(t)

	x := recordedCallOf("Greetings", &pb.HiReq{})
	x.resHeader = metadata.Pairs("h", "\xff", "h", "v", "grpc-x", "y")

	s, err := c.Response(x, nil)
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"h": {"v"}}, s.Header)
}

func TestRecordCodec_FailsOnMessagesItCantWrite(t *testing.T) {
	c := codecOf(t)
	bad := &pb.HiReq{Message: "\xff"}

	_, err := c.Request(recordedCallOf("Greetings", bad), nil)
	require.Error(t, err)

	_, err = c.Request(recordedCallOf("Upload", &pb.ListItem{}, &pb.ListItem{Key: "\xff"}), nil)
	require.ErrorContains(t, err, "message 1:")

	x := recordedCallOf("Greetings", &pb.HiReq{})
	x.messages = []proto.Message{&pb.HiRes{Message: "\xff"}}
	_, err = c.Response(x, nil)
	require.ErrorContains(t, err, "answer 0:")

	x = recordedCallOf("Greetings", &pb.HiReq{})
	st, err := status.New(codes.NotFound, "x").WithDetails()
	require.NoError(t, err)

	p := st.Proto()
	p.Details = append(p.Details, &anypb.Any{TypeUrl: "type.googleapis.com/unknown.Type"})
	x.status = status.FromProto(p)
	_, err = c.Response(x, nil)
	require.ErrorContains(t, err, "status detail 0:")

	_, err = c.message(nil)
	require.Error(t, err)
}
