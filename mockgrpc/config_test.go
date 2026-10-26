package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestNoNearMisses_LeavesTheClosestMocksOutOfTheJournal(t *testing.T) {
	// The report runs the matchers matching never reached: a server with many mocks, or matchers with actions, turns
	// it off, and the entry of a call no mock matched then names no mock, where it would name the closest.
	tests := []struct {
		name     string
		setup    *ConfigBuilder
		reported bool
	}{
		{
			name:     "by default",
			setup:    Setup(),
			reported: true,
		},
		{
			name:     "turned off",
			setup:    Setup().NoNearMisses(),
			reported: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, c := journaled(t, tt.setup)
			scope := m.MustMock(Method("Greetings").Field("message", match.Equal("hi")).Reply(Message(&pb.HiRes{})))

			_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "bye"})
			require.Equal(t, codes.NotFound, status.Code(err))

			var want []vlatte.NearMiss
			if tt.reported {
				want = []vlatte.NearMiss{{MockID: scope.IDs()[0], Passed: 1, Total: 2, Failed: []string{"field message"}}}
			}

			e := onlyCall(t, m, journal.Query{Outcome: journal.OutcomeUnmatched})
			require.Equal(t, want, e.NearMisses)
		})
	}
}

func TestSetup_OptionsCompose(t *testing.T) {
	// Each Setup set every setting, defaults included, so a later one reset what an earlier one set. A later one
	// changes only what it sets now: here, the code of the first and the debug headers of the second both hold.
	m := MustNew(Setup().Silent().NoMatchCode(codes.Unimplemented), Setup().NoDebugHeaders())
	m.MustStart()
	t.Cleanup(m.Close)

	var header metadata.MD
	_, err := pb.NewTestServiceClient(dial(t, m)).Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))
	require.Equal(t, codes.Unimplemented, status.Code(err))

	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	_, err = pb.NewTestServiceClient(dial(t, m)).Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))
	require.NoError(t, err)
	require.Empty(t, header.Get(vlatte.MatchedMockIDHeader), "no debug headers")
}
