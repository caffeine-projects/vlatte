package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

// A method name matched anywhere in the full method, so a mock for Greet answered Greetings, and one for Lis answered
// List. A name names whole segments now: the full method, or its end after a "/" or a ".", so the method, its service
// and its package are written whole. A name that names no method fails the mock, where it used to answer nothing.

func TestMethod_NamesWholeSegments(t *testing.T) {
	for _, name := range []string{
		"Greetings",
		"TestService/Greetings",
		"protobuf.TestService/Greetings",
		"/protobuf.TestService/Greetings",
		"/Greetings",
		pb.TestService_Greetings_FullMethodName,
	} {
		t.Run(name, func(t *testing.T) {
			m, client := methodServer(t)
			m.MustMock(Method(name).Reply(Message(&pb.HiRes{Message: "bye"})))

			res, err := client.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
			require.NoError(t, err)
			require.Equal(t, "bye", res.GetMessage())
		})
	}
}

func TestMethod_APartOfANameFailsTheMock(t *testing.T) {
	// A typo, or a part of a name, would leave the mock waiting for calls that never come: the mock fails instead.
	for _, name := range []string{"Greet", "etings", "Service/Greetings", "Lis", "TestService/Nope"} {
		t.Run(name, func(t *testing.T) {
			m, _ := methodServer(t)

			_, err := m.Mock(Method(name).Reply(Message(&pb.HiRes{})))
			require.ErrorIs(t, err, ErrUnknownMethod)
		})
	}
}

func TestMethod_ANameOfMethodsOfDifferentTypesFailsTheMock(t *testing.T) {
	// Inspect is unary in TestService and client streaming in OtherService: one mock can't answer both, so the name
	// must say which.
	m, _ := methodServer(t)

	_, err := m.Mock(Method("Inspect").Reply(Message(&pb.Rich{})))
	require.ErrorIs(t, err, ErrAmbiguousMethod)

	_, err = m.Mock(Method("TestService/Inspect").Reply(Message(&pb.Rich{})))
	require.NoError(t, err)
}

func TestMethod_ANameOfMethodsOfOneTypeServesThemAll(t *testing.T) {
	// Greetings is unary in both services: one mock answers the calls of both.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scope := m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "bye"})))
	require.Equal(t, []string{pb.OtherService_Greetings_FullMethodName, pb.TestService_Greetings_FullMethodName},
		scope.Mocks()[0].Methods())

	conn := dial(t, m)

	_, err := pb.NewTestServiceClient(conn).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	_, err = pb.NewOtherServiceClient(conn).Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)

	require.Equal(t, int64(2), scope.Hits())
}

// methodServer starts a server of the test service, and returns it with a client of it.
func methodServer(t *testing.T) (*Server, pb.TestServiceClient) {
	t.Helper()

	m := MustNew(Setup().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	m.MustStart()
	t.Cleanup(m.Close)

	return m, pb.NewTestServiceClient(dial(t, m))
}

// dial returns a client connection to m, which the test closes.
func dial(t *testing.T, m *Server) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient(m.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}
