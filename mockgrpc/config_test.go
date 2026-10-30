package mockgrpc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
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
	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))
	require.Equal(t, codes.Unimplemented, status.Code(err))

	m.MustMock(Method("Greetings").Reply(Message(&pb.HiRes{})))

	_, err = pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{}, grpc.Header(&header))
	require.NoError(t, err)
	require.Empty(t, header.Get(vlatte.HeaderMatchedMockID), "no debug headers")
}

func TestConfig_SetsEverySettingItsZeroValuesIncluded(t *testing.T) {
	// A Config option replaces what options before it set: the no-match code of the first is gone.
	logger := zerolog.Nop()
	m := MustNew(Setup().NoMatchCode(codes.Unimplemented), &Config{Logger: &logger, NoMatchCode: codes.Aborted, ConfigSources: []ConfigSource{}})
	m.MustStart()
	t.Cleanup(m.Close)

	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.Aborted, status.Code(err))
}

func TestSetup_InboundTimeoutsComposeAndConfigCanResetThem(t *testing.T) {
	setup := Setup().ConfigSources().ConnectionTimeout(time.Second)
	idle := Setup().MaxConnectionIdle(2 * time.Second)
	m := MustNew(setup, idle)
	t.Cleanup(m.Close)
	require.Equal(t, time.Second, m.Config().ConnectionTimeout)
	require.Equal(t, 2*time.Second, m.Config().MaxConnectionIdle)

	reset := MustNew(setup, idle, &Config{ConfigSources: []ConfigSource{}})
	t.Cleanup(reset.Close)
	require.Equal(t, time.Duration(0), reset.Config().ConnectionTimeout)
	require.Equal(t, time.Duration(0), reset.Config().MaxConnectionIdle)
}

func TestSetup_AListenerNameTwiceFailsNew(t *testing.T) {
	noop := vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return nil })

	_, err := New(Setup().OnCompleteListener("a", noop).OnCompleteListener("a", noop))
	require.ErrorContains(t, err, "config: on_complete listener a:")
}

func TestSetup_FilesServeServicesWithoutGeneratedCode(t *testing.T) {
	// A registry of descriptors, as Files gives it, serves as a descriptor set file does.
	files, err := loadDescriptors([]string{writeDescriptorSet(t, "dyn")})
	require.NoError(t, err)

	without := MustNew(Setup().Silent())
	_, err = without.Mock(Method("dyn.TestService/Greetings").Reply(JSON(`{}`)))
	require.ErrorIs(t, err, ErrUnknownMethod)

	with := MustNew(Setup().Silent().Files(files))
	_, err = with.Mock(Method("dyn.TestService/Greetings").Reply(JSON(`{}`)))
	require.NoError(t, err)
}

func TestSetup_NameTagsTheLogLinesOfTheServer(t *testing.T) {
	// Several servers log to the same output: each line names the server that wrote it.
	m := MustNew(Setup().Name("orders").Admin(admin.MustNew()).LogLevel(vlatte.LogLevelWarn))
	m.MustStart()
	t.Cleanup(m.Close)

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.AdminURL()+"/logs", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	_, err = pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.NotFound, status.Code(err))

	lines := bufio.NewScanner(res.Body)
	for lines.Scan() {
		if strings.Contains(lines.Text(), "no mock matched the call") {
			require.Contains(t, lines.Text(), `"server":"orders"`)
			return
		}
	}

	t.Fatal("no log line of the call")
}

// syncBuffer is a buffer a server writes to while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestSetup_LogOutputTakesTheLogsOfTheLoggerItBuilds(t *testing.T) {
	// A program that keeps the standard output for something else, as vlatte mcp keeps it for MCP, logs elsewhere.
	var out syncBuffer
	m := MustNew(Setup().LogOutput(&out).LogLevel(vlatte.LogLevelWarn))
	m.MustStart()
	t.Cleanup(m.Close)

	_, err := pb.NewTestServiceClient(m.MustDial()).Greetings(t.Context(), &pb.HiReq{})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Contains(t, out.String(), "no mock matched the call")
}

// _customMatchers holds two custom matchers: [upper], a text in upper case, and [greets, name], a call of Greetings
// whose request greets name, which reads the whole call, as request.custom gives it.
var _customMatchers = matchbuild.MustNew(
	matchbuild.WithMatcher("upper", func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
		if len(args) != 0 {
			return nil, errors.New("takes no arguments")
		}

		return match.Function(func(v any) (bool, error) {
			s, ok := v.(string)
			return ok && s != "" && s == strings.ToUpper(s), nil
		}), nil
	}),
	matchbuild.WithMatcher("greets", func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
		if len(args) != 1 {
			return nil, errors.New("takes a name")
		}

		name, ok := args[0].(string)
		if !ok {
			return nil, errors.New("takes a name")
		}

		return match.Function(func(v any) (bool, error) {
			call, isCall := v.(*Call)
			if !isCall {
				return false, nil
			}

			req, isHi := call.Message().(*pb.HiReq)

			return isHi && req.GetMessage() == name, nil
		}), nil
	}),
)

func TestSetup_MatchersBuildTheCustomMatchersOfMockFiles(t *testing.T) {
	// A mock file names a custom matcher of the server's registry wherever it takes a matcher list: request.custom
	// gives it the whole call, and the other keys what they match, such as a field or a bidi message.
	m := fileServer(t, Setup().Matchers(_customMatchers))
	m.MustMock(
		FromBytes([]byte(`
request: {method: Greetings, fields: {message: [upper]}}
response: {message: {message: loud}}
`), "yaml"),
		FromBytes([]byte(`
request: {method: Greetings, custom: [[greets, ann]]}
response: {message: {message: hi ann}}
`), "yaml"),
		FromBytes([]byte(`
request: {method: Chat}
bidi:
  on: [{when: [field, message, [upper]], response: {message: {message: loud}}}]
  otherwise: {message: {message: quiet}}
`), "yaml"),
	)

	c := pb.NewTestServiceClient(m.MustDial())
	for give, want := range map[string]string{"HI": "loud", "ann": "hi ann"} {
		res, err := c.Greetings(t.Context(), &pb.HiReq{Message: give})
		require.NoError(t, err, give)
		require.Equal(t, want, res.GetMessage(), give)
	}

	_, err := c.Greetings(t.Context(), &pb.HiReq{Message: "bob"})
	require.Equal(t, codes.NotFound, status.Code(err))

	stream, err := c.Chat(t.Context())
	require.NoError(t, err)

	for give, want := range map[string]string{"HEY": "loud", "hey": "quiet"} {
		require.NoError(t, stream.Send(&pb.HiReq{Message: give}))

		res, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, want, res.GetMessage(), give)
	}

	require.NoError(t, stream.CloseSend())
}

func TestSetup_MatchersBelongToTheirServer(t *testing.T) {
	// A registry is the server's, not global: a server without it doesn't know its matchers, and says which.
	fileServer(t, Setup().Matchers(_customMatchers))
	m := fileServer(t, Setup())

	_, err := m.Mock(FromBytes([]byte(`{request: {method: Greetings, custom: [[greets, ann]]}, response: {}}`), "yaml"))
	require.ErrorContains(t, err, `unknown matcher "greets"`)
}

func TestSetup_KVIsTheStoreTheServerReads(t *testing.T) {
	// A store given in Go, such as one on Redis, is the very store the server reads, not a copy of it: a value set on
	// it after the server started is what templates render.
	kv := vlatte.NewMemoryKV()
	m := MustNew(Setup().Silent().Service(&pb.TestService_ServiceDesc, &pb.UnimplementedTestServiceServer{}).KV(kv))
	m.MustStart()
	t.Cleanup(m.Close)

	require.Same(t, kv, m.KV())
	require.Same(t, kv, m.Config().KV)

	m.MustMock(Method("Greetings").Reply(Template(`{"message": "{{ .KV.Get "greeting" }}"}`)))
	require.NoError(t, kv.Set(t.Context(), "greeting", []byte("hello")))

	got, code := greetWith(t, client(t, m), "ann")
	require.Equal(t, codes.OK, code)
	require.Equal(t, "hello", got)
}
