package mockgrpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestEcho_AnswersEachCallTypeWithWhatTheClientSent(t *testing.T) {
	m := serve(t)
	c := client(t, m)
	m.MustMock(
		Method("Greetings").Reply(Echo()),
		Method("List").Reply(Echo()),
		Method("Upload").Reply(Echo()),
		Method("Chat").Reply(Echo()),
	)

	got, code := greetWith(t, c, "hi")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hi", got)

	stream, err := c.List(t.Context(), &pb.ListReq{Message: "k"})
	require.NoError(t, err)

	item, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "k", item.GetKey(), "a field comes back by its number: ListReq.message is ListItem.key")

	upload := func(keys ...string) string {
		up, err := c.Upload(t.Context())
		require.NoError(t, err)

		for _, k := range keys {
			require.NoError(t, up.Send(&pb.ListItem{Key: k}))
		}

		res, err := up.CloseAndRecv()
		require.NoError(t, err)

		return res.GetMessage()
	}

	require.Equal(t, "b", upload("a", "b"), "a client stream answers its last message")
	require.Empty(t, upload(), "a client stream that sent nothing answers an empty message")

	said, code := chat(t, c, nil, "one", "two")
	require.Equal(t, codes.OK, code)
	require.Equal(t, []string{"one", "two"}, said)
}

func TestEcho_LogLogsEachMessageItEchoes(t *testing.T) {
	var out syncBuffer

	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).LogOutput(&out))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").Reply(Echo().Log()), Method("Chat").Reply(Echo()))

	greetWith(t, client(t, m), "logged")
	chat(t, client(t, m), nil, "unlogged")

	logs := out.String()
	require.Contains(t, logs, `"method":"/protobuf.TestService/Greetings","message":{"message":"logged"}`)
	require.Equal(t, 1, strings.Count(logs, `"message":"echo"`), "only Log's echoes are logged: %s", logs)
}

func TestEcho_AMessageThatIsNoOutputFailsTheCall(t *testing.T) {
	// The bytes of a request whose field 1 is bytes don't read as a message whose field 1 is text, unless they are
	// UTF-8: the client gets the failure of the reply, not a message the server made up.
	set := &descriptorpb.FileDescriptorSet{}
	b, err := os.ReadFile(writeDescriptorSet(t, "raw"))
	require.NoError(t, err)
	require.NoError(t, proto.Unmarshal(b, set))

	for _, msg := range set.GetFile()[0].GetMessageType() {
		if msg.GetName() == "HiReq" {
			msg.GetField()[0].Type = descriptorpb.FieldDescriptorProto_TYPE_BYTES.Enum()
		}
	}

	b, err = proto.Marshal(set)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "raw.binpb")
	require.NoError(t, os.WriteFile(path, b, 0o600))

	m := MustNew(Setup().Silent().Descriptors(path))
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Method("raw.TestService/Greetings").Reply(Echo()), Method("raw.TestService/Chat").Reply(Echo()))

	files, err := loadDescriptors([]string{path})
	require.NoError(t, err)

	desc := func(name string) protoreflect.MessageDescriptor {
		d, err := files.FindDescriptorByName(protoreflect.FullName(name))
		require.NoError(t, err)

		return d.(protoreflect.MessageDescriptor)
	}

	req := dynamicpb.NewMessage(desc("raw.HiReq"))
	req.Set(req.Descriptor().Fields().ByName("message"), protoreflect.ValueOfBytes([]byte{0xff}))

	var trailer metadata.MD
	err = m.MustDial().Invoke(t.Context(), "/raw.TestService/Greetings", req, dynamicpb.NewMessage(desc("raw.HiRes")), grpc.Trailer(&trailer))
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))
	require.Contains(t, status.Convert(err).Message(), "reply_echo: the message is no raw.HiRes")

	stream, err := m.MustDial().NewStream(t.Context(), &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/raw.TestService/Chat")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(req))

	err = stream.RecvMsg(dynamicpb.NewMessage(desc("raw.HiRes")))
	require.Equal(t, codes.Internal, status.Code(err), "a bidi call ends at the message that fails")
}
