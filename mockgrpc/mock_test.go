package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"

	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestMock_IncCountsCallsReportedByAnExternalHandler(t *testing.T) {
	m := MustNew(Setup().Silent().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}))
	mock, err := Method("Greetings").Reply(Message(&pb.HiRes{})).Build(m)
	require.NoError(t, err)
	require.False(t, mock.Called())
	mock.Inc()
	mock.Inc()
	require.Equal(t, int64(2), mock.Hits())
	require.True(t, mock.Called())
}
