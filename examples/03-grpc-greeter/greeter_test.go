package greeter_test

// The docs home page shows the imports and TestGreeter, from here to the end of the region.
// #region example
import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/examples/03-grpc-greeter/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestGreeter(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method("greeter.Greeter/SayHello").
		Field("name", match.HasPrefix("a")).
		Reply(mockgrpc.Message(&pb.HelloReply{Message: "hi ann"})))

	client := pb.NewGreeterClient(m.MustDial())
	res, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "ann"})

	require.NoError(t, err)
	require.Equal(t, "hi ann", res.GetMessage())
}

// #endregion example
