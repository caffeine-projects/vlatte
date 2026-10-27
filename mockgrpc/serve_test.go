package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServer_ServesAServiceNoOneRegistered(t *testing.T) {
	// Mocking a service took registering an implementation of it, such as pb.UnimplementedTestServiceServer, on the
	// server: a server without it answered Unimplemented, whatever its mocks. Its generated code is enough now.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		Method("Greetings").Reply(Message(&pb.HiRes{Message: "unary"})),
		Method("List").Reply(Messages(&pb.ListItem{Key: "a"})),
		Method("Upload").Reply(Message(&pb.HiRes{Message: "client"})),
	)

	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "hi")
	require.Equal(t, "unary", got)
	require.Equal(t, []string{"a"}, listKeys(t, c))

	got, _ = upload(t, c, "x")
	require.Equal(t, "client", got)
}

func TestServer_AnUnknownMethodIsUnimplemented(t *testing.T) {
	// No mock can answer a method no descriptor describes: the answer says so, as the server's own.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	var trailer metadata.MD
	err := m.MustDial().Invoke(t.Context(), "/nope.Service/Call", &pb.HiReq{}, &pb.HiRes{}, grpc.Trailer(&trailer))
	require.Equal(t, codes.Unimplemented, status.Code(err))
	require.Equal(t, []string{vlatte.FailureNoMatch}, trailer.Get(vlatte.HeaderFailure))
}

func TestServer_AScenarioMovesBeforeItsMockAnswers(t *testing.T) {
	// A client that gets the answer and calls again at once must match in the new state: a call sent while the mock
	// still answers already does.
	m := serve(t)
	c := client(t, m)

	during := make(chan string, 1)
	m.MustMock(
		Method("Greetings").StartScenario("checkout").ScenarioStateWillBe("paid").
			Reply(funcUnaryReply(func() (any, error) {
				got, _ := greetWith(t, c, "")
				during <- got
				return &pb.HiRes{Message: "pay"}, nil
			})),
		Method("Greetings").ScenarioIs("checkout").ScenarioStateIs("paid").Reply(Message(&pb.HiRes{Message: "receipt"})),
	)

	got, code := greetWith(t, c, "")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "pay", got)
	require.Equal(t, "receipt", <-during, "the receipt of paid answered while pay still answered")
}
