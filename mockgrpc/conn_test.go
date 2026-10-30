package mockgrpc

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestDial_ReachesTheServerAndClosesWithIt(t *testing.T) {
	// A test dialed the server itself, and closed the connection itself, in every test.
	m := MustNew(Setup().Silent())

	_, err := m.Dial()
	require.ErrorIs(t, err, errNotStarted, "nothing to dial before Start")

	m.MustStart()
	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{Message: "bye"})))

	conn := m.MustDial()
	got, _ := greetWith(t, pb.NewTestServiceClient(conn), "hi")
	require.Equal(t, "bye", got)

	m.Close()
	require.Equal(t, connectivity.Shutdown, conn.GetState(), "Close closed the connection")

	_, err = m.Dial()
	require.ErrorIs(t, err, grpc.ErrServerStopped, "a closed server serves no more")
}

func TestInMemory_ServesWithoutAPort(t *testing.T) {
	// Tests that run at once need no free port, and nothing outside the test reaches the server.
	m := MustNew(Setup().Silent().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("List").Reply(Messages(&pb.ListItem{Key: "a"}, &pb.ListItem{Key: "b"})))

	c := pb.NewTestServiceClient(m.MustDial())
	require.Equal(t, []string{"a", "b"}, listKeys(t, c))

	_, code := greetWith(t, c, "unmocked")
	require.Equal(t, codes.NotFound, code)
}

func TestInMemory_RefusesAListener(t *testing.T) {
	// An in-memory server serves no listener: one given too was neither served nor closed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	_, err = New(Setup().InMemory().Listener(l))
	require.ErrorIs(t, err, errInMemoryListener)
}

func TestDial_InvalidCredentialsFailBeforeACall(t *testing.T) {
	m := MustNew(Setup().Silent().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)
	_, err := m.Dial(grpc.WithTransportCredentials(nil))
	require.EqualError(t, err, "grpc: no transport security set (use grpc.WithTransportCredentials(insecure.NewCredentials()) explicitly or set credentials)")
	require.PanicsWithError(t, err.Error(), func() { m.MustDial(grpc.WithTransportCredentials(nil)) })
}
