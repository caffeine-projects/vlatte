package mockgrpc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/caffeine-projects/vlatte"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestOnComplete_GivesListenersTheCallAndTheServersTools(t *testing.T) {
	// A listener, such as a webhook, renders its templates with the server's engine and the call's data, opens its
	// files from the mock file's directory, and logs with the server's logger.
	type seen struct {
		dir      string
		logger   *zerolog.Logger
		engine   vlatte.TemplateEngine
		message  any
		server   *Server
		method   protoreflect.FullName
		mockName string
	}

	got := make(chan seen, 1)
	listeners := vlatte.NewListener()
	require.NoError(t, listeners.OnComplete.Register("inspect", vlatte.EventFunc[vlatte.OnComplete](
		func(_ context.Context, e vlatte.OnComplete) error {
			oc := e.(*OnComplete)
			got <- seen{
				dir:      oc.Dir(),
				logger:   oc.Logger(),
				engine:   oc.Kit().TemplateEngine(),
				message:  oc.Kit().Data().(*TemplateData).Request.Message,
				server:   oc.Call().Server(),
				method:   oc.Call().Descriptor().FullName(),
				mockName: oc.Mock().(*Mock).Name(),
			}

			return nil
		})))

	engine, err := vlatte.NewGoTemplateEngine()
	require.NoError(t, err)

	logger := zerolog.Nop()
	m := MustNew(Setup().Logger(&logger).EventListeners(listeners).TemplateEngine(engine))
	m.MustStart()
	t.Cleanup(m.Close)

	dir := t.TempDir()
	path := writeFile(t, dir, "greet.yaml", `
name: greet
on_complete: [{name: inspect}]
request: {method: Greetings}
response: {message: {message: hello}}
`)
	m.MustMock(FromFile(path))

	_, err = pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)

	require.Equal(t, seen{
		dir:      filepath.Dir(path),
		logger:   &logger,
		engine:   engine,
		message:  map[string]any{"message": "hi"},
		server:   m,
		method:   "protobuf.TestService.Greetings",
		mockName: "greet",
	}, <-got)
}
