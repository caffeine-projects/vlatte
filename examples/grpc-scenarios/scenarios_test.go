package scenarios_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

func TestFileMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().InMemory().MockFiles("testdata/lifecycle.mock.yaml"))
	m.MustStart()
	t.Cleanup(m.Close)

	client := pb.NewTrackingClient(m.MustDial())
	requireParcelState(t, client, "P1", "in_transit")
	requireParcelState(t, client, "P1", "delivered")
	requireParcelState(t, client, "P1", "delivered")
	requireParcelState(t, client, "P2", "in_transit")

	m.ResetScenario("tracking", matchscenario.Key("P1"))
	requireParcelState(t, client, "P1", "in_transit")
	requireParcelState(t, client, "P2", "delivered")
}

func TestProgrammaticMocks(t *testing.T) {
	m := mockgrpc.MustNew(mockgrpc.Setup().ConfigSources().InMemory())
	m.MustStart()
	t.Cleanup(m.Close)

	const key = `{{ .Request.Message.id }}`
	const reply = `{"state": "{{ .Scenarios.StateOf "tracking" .Request.Message.id }}"}`

	m.MustMock(
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			StartScenario("tracking").ScenarioKey(key).
			ScenarioStateWillBe("in_transit").Reply(mockgrpc.Template(reply)),
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			ScenarioIs("tracking").ScenarioKey(key).ScenarioStateIs("in_transit").
			ScenarioStateWillBe("delivered").Reply(mockgrpc.Template(reply)),
		mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
			ScenarioIs("tracking").ScenarioKey(key).ScenarioStateIs("delivered").
			Reply(mockgrpc.Template(reply)),
	)

	client := pb.NewTrackingClient(m.MustDial())
	requireParcelState(t, client, "P1", "in_transit")
	requireParcelState(t, client, "P1", "delivered")
	requireParcelState(t, client, "P1", "delivered")
	requireParcelState(t, client, "P2", "in_transit")

	m.ResetScenario("tracking", matchscenario.Key("P1"))
	requireParcelState(t, client, "P1", "in_transit")
	requireParcelState(t, client, "P2", "delivered")
}

func requireParcelState(t *testing.T, client pb.TrackingClient, id, want string) {
	t.Helper()

	reply, err := client.GetParcel(t.Context(), &pb.ParcelRef{Id: id})
	require.NoError(t, err)
	require.Equal(t, want, reply.GetState(), "parcel %s", id)
}
