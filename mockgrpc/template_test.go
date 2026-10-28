package mockgrpc

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestServer_SetDataIsWhatTemplatesReadAsData(t *testing.T) {
	// A test points a mock at what it starts after the server, such as another server's address: each call reads the
	// data in force when it arrives, and the server keeps its own copy of the map.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ .Data.target }}"}`)))
	c := pb.NewTestServiceClient(m.MustDial())

	data := map[string]any{"target": "first"}
	m.SetData(data)
	data["target"] = "changed after SetData"

	res, err := c.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "first", res.GetMessage())
	require.Equal(t, map[string]any{"target": "first"}, m.Data())

	m.SetData(map[string]any{"target": "second"})

	res, err = c.Greetings(t.Context(), &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "second", res.GetMessage())
}

func TestServer_SetDataWhileServing(t *testing.T) {
	// Calls read the data while a test sets it, so SetData and Data must be safe for concurrent use.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ index .Data "n" }}"}`)))
	c := pb.NewTestServiceClient(m.MustDial())
	m.SetData(map[string]any{"n": -1})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { m.SetData(map[string]any{"n": i}) })
		wg.Go(func() {
			_, err := c.Greetings(t.Context(), &pb.HiReq{})
			assert.NoError(t, err)
		})
	}

	wg.Wait()
}

// provider is a TemplateDataProvider that gives what its function returns.
type provider struct {
	key  string
	data func(in *TemplateDataProviderInput) (any, error)
}

func (p provider) Key() string { return p.key }

func (p provider) Data(in *TemplateDataProviderInput) (any, error) { return p.data(in) }

func TestTemplateDataProviders(t *testing.T) {
	serve := func(t *testing.T, providers ...TemplateDataProvider) (*Server, pb.TestServiceClient) {
		m := MustNew(Setup().Silent().TemplateDataProviders(providers...))
		m.MustStart()
		t.Cleanup(m.Close)

		return m, pb.NewTestServiceClient(m.MustDial())
	}

	t.Run("templates read what providers give, by key", func(t *testing.T) {
		m, c := serve(t, provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) {
			return map[string]any{"name": "ana"}, nil
		}})
		m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ .Provided.user.name }}"}`)))

		res, err := c.Greetings(t.Context(), &pb.HiReq{})
		require.NoError(t, err)
		require.Equal(t, "ana", res.GetMessage())
	})

	t.Run("providers see the call and the mock that matched", func(t *testing.T) {
		m, c := serve(t, provider{key: "seen", data: func(in *TemplateDataProviderInput) (any, error) {
			return in.Mock().ID() + " " + in.Call().Header().Get("x-team")[0], nil
		}})
		m.MustMock(Method("Greetings").ID("greet").Reply(Template(`{"message": "{{ .Provided.seen }}"}`)))

		res, err := c.Greetings(metadata.AppendToOutgoingContext(t.Context(), "x-team", "core"), &pb.HiReq{})
		require.NoError(t, err)
		require.Equal(t, "greet core", res.GetMessage())
	})

	t.Run("a provider that fails fails the call, and its mock doesn't count it", func(t *testing.T) {
		m, c := serve(t, provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) {
			return nil, errors.New("user service down")
		}})
		scope := m.MustMock(Method("Greetings").ID("greet").Reply(Template(`{"message": "{{ .Provided.user }}"}`)))

		var trailer metadata.MD
		_, err := c.Greetings(t.Context(), &pb.HiReq{}, grpc.Trailer(&trailer))
		require.Equal(t, codes.Internal, status.Code(err))
		require.Contains(t, status.Convert(err).Message(), "template data provider user: user service down")
		require.Equal(t, []string{vlatte.FailureReply}, trailer.Get(vlatte.HeaderFailure))
		require.Zero(t, scope.Mock("greet").Hits())
	})

	t.Run("two providers with one key fail the server", func(t *testing.T) {
		// Templates could read only one of them, so this is a setup mistake, not a warning on each call.
		same := provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) { return nil, nil }}

		_, err := New(Setup().Silent().TemplateDataProviders(same, same))
		require.ErrorIs(t, err, vlatte.ErrTemplateDataKeyExists)
		require.ErrorContains(t, err, `"user"`)
	})
}
