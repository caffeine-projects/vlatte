package mockgrpc

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	pb "github.com/caffeine-projects/vlatte/mockgrpc/internal/protobuf"
)

func TestCodeOf_ReadsTheNamesGRPCWritesAndDescriptionsWrite(t *testing.T) {
	// A description writes a code by its name: every name loads back as its code, as do the Go names and numbers.
	for c := range codes.Code(17) {
		for _, give := range []any{codeName(c), c.String(), int64(c)} {
			got, err := codeOf(config.Value{Key: "code", Any: give})
			require.NoError(t, err, "%v", give)
			require.Equal(t, c, got, "%v", give)
		}
	}

	require.Equal(t, "OK", codeName(codes.OK))
	require.Equal(t, "DEADLINE_EXCEEDED", codeName(codes.DeadlineExceeded))

	_, err := codeOf(config.Value{Key: "code", Any: int64(17)})
	require.Error(t, err)
}

func TestMockFile_ReadsEveryKeyOfAMock(t *testing.T) {
	// Each key of a mock file sets what its Builder call sets: a disabled mock never answers, and the one that does
	// answers once, in its scenario, and runs its event.
	got := make(chan any, 1)
	record := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, e vlatte.OnComplete) error {
		got <- e.Parameters()
		return nil
	})
	m := fileServer(t, Setup().OnCompleteListener("record", record))

	_, err := m.Mock(FromBytes([]byte(`
id: off
enabled: false
request: {method: Greetings}
response: {message: {message: disabled}}
`), "yaml"))
	require.NoError(t, err)

	_, err = m.Mock(FromBytes([]byte(`
id: full
name: greet
priority: 1
delay: 1ms
repeat: 1
scenario: {name: s, required_state: started, new_state: done}
on_complete: [{name: record, parameters: from-file}]
request:
  method: Greetings
  body: [contains, hi]
response: {message: {message: hello}}
`), "yaml"))
	require.NoError(t, err)

	c := pb.NewTestServiceClient(m.MustDial())
	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "bye"})
	require.Equal(t, codes.NotFound, status.Code(err))

	res, err := c.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.NoError(t, err)
	require.Equal(t, "hello", res.GetMessage())
	require.Equal(t, "from-file", <-got)

	_, err = c.Greetings(t.Context(), &pb.HiReq{Message: "hi"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestMockFile_BidiTrailerEndsTheStream(t *testing.T) {
	m := fileServer(t, Setup())
	m.MustMock(FromBytes([]byte(`
request: {method: Chat}
bidi:
  header: {h: "1"}
  trailer: {t: ["2", "3"]}
  on_end: {message: {message: bye}}
`), "yaml"))

	stream, err := pb.NewTestServiceClient(m.MustDial()).Chat(t.Context())
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())

	res, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "bye", res.GetMessage())

	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, metadata.MD{"t": {"2", "3"}}, stream.Trailer())
}

func TestMockFile_NamesTheKeyOfEachProblem(t *testing.T) {
	// A mock file that can't load says which key is wrong, so its author fixes it without reading the code.
	for _, tc := range []struct {
		name string
		doc  string
		key  string
	}{
		{"repeat below one", `{repeat: 0, request: {method: Greetings}, response: {}}`, "repeat"},
		{"scenario without a name", `{scenario: {required_state: a}, request: {method: Greetings}, response: {}}`, "scenario.name"},
		{"event without a name", `{on_complete: [{parameters: 1}], request: {method: Greetings}, response: {}}`, "on_complete[0].name"},
		{"event that is no object", `{on_complete: [1], request: {method: Greetings}, response: {}}`, "on_complete[0]"},
		{"messages that are no list", `{request: {method: Upload, messages: {key: a}}, response: {}}`, "request.messages"},
		{"body that is an empty list", `{request: {method: Greetings, body: []}, response: {}}`, "request.body"},
		{"body that is no matcher", `{request: {method: Greetings, body: 1}, response: {}}`, "request.body"},
		{"field that is no matcher", `{request: {method: Greetings, fields: {message: 1}}, response: {}}`, "request.fields.message"},
		{"detail that is no object", `{request: {method: Greetings}, response: {status: {code: 5, details: [1]}}}`, "response.status.details[0]"},
		{"detail without its type", `{request: {method: Greetings}, response: {status: {code: 5, details: [{reason: R}]}}}`, "response.status.details[0]"},
		{"header that is no string", `{request: {method: Chat}, bidi: {header: {h: 1}}}`, "bidi.header.h"},
		{"header item that is no string", `{request: {method: Chat}, bidi: {trailer: {t: [1]}}}`, "bidi.trailer.t[0]"},
		{"rule without a response", `{request: {method: Chat}, bidi: {on: [{when: [contains, a]}]}}`, "bidi.on[0]"},
		{"rule that is no object", `{request: {method: Chat}, bidi: {on: [1]}}`, "bidi.on[0]"},
		{"code that is no name or number", `{request: {method: Greetings}, response: {status: {code: [5]}}}`, "response.status.code"},
		{"messages JSON can't write", `{request: {method: Upload, messages: [{key: .nan}]}, response: {}}`, "request.messages"},
		{"body file that is missing", `{request: {method: Greetings}, response: {body_file: nope.json}}`, "response.body_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fileServer(t, Setup())

			_, err := m.Mock(FromBytes([]byte(tc.doc), "yaml"))
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)

			keys := make([]string, 0, len(cfgErr.Problems))
			for _, p := range cfgErr.Problems {
				keys = append(keys, p.Key)
			}

			require.Equal(t, []string{tc.key}, keys, "%v", err)
		})
	}
}

func TestMockFile_ABodyFileThatIsNoJSONSaysSo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "items.json", "not json")
	path := writeFile(t, dir, "list.yaml", `{request: {method: List}, response: {body_file: items.json}}`)

	_, err := fileServer(t, Setup()).Mock(FromFile(path))
	require.ErrorContains(t, err, "is not JSON")
}
