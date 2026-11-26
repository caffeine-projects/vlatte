package extensions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/examples/grpc-rpc-types/pb"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockgrpc"
)

var _ mockgrpc.RequestParser = scannerID{}

type scannerID struct{}

func (scannerID) CanParse(call *mockgrpc.Call, msg proto.Message) bool {
	_, ok := msg.(*pb.ParcelRef)
	return call.Method() == pb.Tracking_GetParcel_FullMethodName && ok
}

func (scannerID) Parse(_ context.Context, _ *mockgrpc.Call, msg proto.Message) (any, error) {
	ref, ok := msg.(*pb.ParcelRef)
	if !ok {
		return nil, errors.New("scanner: expected a parcel reference")
	}

	id, hasPrefix := strings.CutPrefix(ref.GetId(), "scan:")
	if !hasPrefix || id == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid scan id")
	}

	return map[string]string{"parcel_id": id}, nil
}

func TestFileMocks(t *testing.T) {
	audit, listener := auditListener(t)
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().
		RequestParsers(scannerID{}).
		OnCompleteListener("audit", listener).
		MockFiles("testdata/parsed.mock.yaml")
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	client := pb.NewTrackingClient(m.MustDial())
	reply, err := client.GetParcel(t.Context(), &pb.ParcelRef{Id: "scan:P1"})
	require.NoError(t, err)
	require.Equal(t, "found P1", reply.GetState())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "scan:"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid scan id", status.Convert(err).Message())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "P1"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid scan id", status.Convert(err).Message())

	// The synchronous listener has finished when the RPC returns.
	require.Equal(t, 1, len(audit))
	require.Equal(t, "lookup", <-audit)
}

func TestProgrammaticMocks(t *testing.T) {
	audit, listener := auditListener(t)
	config := mockgrpc.Setup().ConfigSources().InMemory().Silent().
		RequestParsers(scannerID{}).
		OnCompleteListener("audit", listener)
	m := mockgrpc.MustNew(config)
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockgrpc.Method(pb.Tracking_GetParcel_FullMethodName).
		Field("parcel_id", match.Equal("P1")).
		OnCompleteEvent(vlatte.EventConfig{Name: "audit", Parameters: "lookup"}).
		Reply(mockgrpc.Func(func(_ context.Context, call *mockgrpc.Call) error {
			// Message holds the original request. Parsed holds the scanner's view.
			ref, isParcelRef := call.Message().(*pb.ParcelRef)
			fields, isFieldMap := call.Parsed()[0].(map[string]string)
			id := fields["parcel_id"]
			if !isParcelRef || !isFieldMap || ref.GetId() != "scan:"+id {
				return errors.New("scanner: expected the original scan id and its parsed parcel id")
			}
			return call.Send(&pb.ParcelStatus{State: "found " + id})
		})))

	client := pb.NewTrackingClient(m.MustDial())
	reply, err := client.GetParcel(t.Context(), &pb.ParcelRef{Id: "scan:P1"})
	require.NoError(t, err)
	require.Equal(t, "found P1", reply.GetState())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "scan:"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid scan id", status.Convert(err).Message())

	_, err = client.GetParcel(t.Context(), &pb.ParcelRef{Id: "P1"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "invalid scan id", status.Convert(err).Message())

	// The synchronous listener has finished when the RPC returns.
	require.Equal(t, 1, len(audit))
	require.Equal(t, "lookup", <-audit)
}

func auditListener(t *testing.T) (<-chan any, vlatte.EventFunc[vlatte.OnComplete]) {
	t.Helper()

	audit := make(chan any, 1)
	listener := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, event vlatte.OnComplete) error {
		select {
		case audit <- event.Parameters():
		default:
			t.Error("audit listener ran more than once")
		}
		return nil
	})

	return audit, listener
}
