package mockgrpc

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
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
		{"proxy with a response", `{request: {method: Greetings}, response: {}, proxy: {target: "localhost:1"}}`, "proxy"},
		{"sequence without responses", `{request: {method: Greetings}, response_sequence: {after_ended: {}}}`, "response_sequence.responses"},
		{"sequence of no responses", `{request: {method: Greetings}, response_sequence: {responses: []}}`, "response_sequence.responses"},
		{"sequence of no list", `{request: {method: Greetings}, response_sequence: {responses: {}}}`, "response_sequence.responses"},
		{"sequence with a response", `{request: {method: Greetings}, response: {}, response_sequence: {responses: [{}]}}`, "response_sequence"},
		{
			"sequence response with two answers", `{request: {method: Greetings}, response_sequence: {responses: [{message: {}, messages: []}]}}`,
			"response_sequence.responses[0].messages",
		},
		{
			"after_ended with two answers", `{request: {method: Greetings}, response_sequence: {responses: [{}], after_ended: {message: {}, messages: []}}}`,
			"response_sequence.after_ended.messages",
		},
		{"random without responses", `{request: {method: Greetings}, response_random: {seed: 1}}`, "response_random.responses"},
		{"random response of an unknown key", `{request: {method: Greetings}, response_random: {responses: [{body: x}]}}`, "response_random.responses[0].body"},
		{
			"random response with two answers", `{request: {method: Greetings}, response_random: {responses: [{message: {}, messages: []}]}}`,
			"response_random.responses[0].messages",
		},
		{"proxy header that is no string", `{request: {method: Greetings}, proxy: {target: "localhost:1", header: {h: 1}}}`, "proxy.header.h"},
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

func TestMockFile_ASequenceAndARandomAnswerAsSeqAndRandDo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "done.json", `{"message": "done"}`)

	seq := writeFile(t, dir, "seq.yaml", `
request: {method: Greetings}
response_sequence:
  responses:
    - message: {message: pending}
    - status: {code: UNAVAILABLE}
  after_ended: {body_file: done.json}
`)

	m := fileServer(t, Setup())
	m.MustMock(FromFile(seq), FromBytes([]byte(`
request: {method: List}
response_random:
  seed: 7
  responses:
    - messages: [{key: a}]
    - messages: [{key: b}]
`), "yaml"))

	c := pb.NewTestServiceClient(m.MustDial())

	got, _ := greetWith(t, c, "")
	require.Equal(t, "pending", got)

	_, code := greetWith(t, c, "")
	require.Equal(t, codes.Unavailable, code)

	got, _ = greetWith(t, c, "")
	require.Equal(t, "done", got)

	same := RandWithSeed(7, Messages(&pb.ListItem{Key: "a"}), Messages(&pb.ListItem{Key: "b"}))
	other := serve(t)
	other.MustMock(Method("List").Reply(same))

	want, picked := make([]string, 0, 20), make([]string, 0, 20)
	for range 20 {
		want = append(want, listKeys(t, client(t, other))...)
		picked = append(picked, listKeys(t, c)...)
	}

	require.Equal(t, want, picked, "a seed in a file picks as RandWithSeed does")
}

func TestMockFile_AProxyGoesOnToItsTarget(t *testing.T) {
	// A mock file proxies as From does: the target gets the call, with the metadata the file sets in place of the
	// client's, and without the keys it removes.
	target := serve(t)
	target.MustMock(Method("Greetings").
		Header("x-token", match.Equal("file's")).
		Header("x-drop", match.Not(match.Present())).
		Reply(Message(&pb.HiRes{Message: "from the target"})))

	m := serve(t)
	m.MustMock(FromBytes([]byte(`
request: {method: Greetings}
proxy:
  target: `+target.Addr()+`
  header: {x-token: file's}
  remove_headers: [x-drop]
`), "yaml"))

	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-token", "client's", "x-drop", "1")
	res, err := client(t, m).Greetings(ctx, &pb.HiReq{})
	require.NoError(t, err)
	require.Equal(t, "from the target", res.GetMessage())
}

func TestMockFile_ABodyFileThatIsNoJSONSaysSo(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "items.json", "not json")
	path := writeFile(t, dir, "list.yaml", `{request: {method: List}, response: {body_file: items.json}}`)

	_, err := fileServer(t, Setup()).Mock(FromFile(path))
	require.ErrorContains(t, err, "is not JSON")
}

// grpc.schema.json is for editors: the decoder doesn't read it. These tests keep the two in step.

const _schemaURL = "mem://vlatte/grpc.schema.json"

func loadJSONSchema(t *testing.T) (*jsonschema.Schema, map[string]any) {
	t.Helper()

	b, err := os.ReadFile("schema/grpc.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	require.NoError(t, c.AddResource(_schemaURL, doc))

	schema, err := c.Compile(_schemaURL)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))

	return schema, raw
}

func TestJSONSchema_KeysMatchTheTables(t *testing.T) {
	// Every key the decoder reads is documented for editors, and nothing else is.
	_, raw := loadJSONSchema(t)

	props := func(node any) map[string]any {
		obj, _ := node.(map[string]any)
		p, _ := obj["properties"].(map[string]any)

		return p
	}

	keys := func(node any, groups ...string) []string {
		var out []string
		for k, v := range props(node) {
			out = append(out, k)
			if slices.Contains(groups, k) {
				for sub := range props(v) {
					out = append(out, k+"."+sub)
				}
			}
		}

		slices.Sort(out)

		return out
	}

	defs, _ := raw["$defs"].(map[string]any)
	bidi := props(raw)["bidi"]
	rule := props(props(bidi)["on"].(map[string]any)["items"])

	require.Equal(t, _mockSchema.Keys(), keys(raw, "scenario", "request"), "mock")
	require.Equal(t, _replySchema.Keys(), keys(defs["reply"]), "reply")
	require.Equal(t, _statusSchema.Keys(), keys(defs["status"]), "status")
	require.Equal(t, _bidiSchema.Keys(), keys(bidi), "bidi")
	require.Equal(t, _ruleSchema.Keys(), slices.Sorted(maps.Keys(rule)), "bidi rule")
	require.Equal(t, _eventSchema.Keys(), keys(defs["on_complete_event"]), "on_complete")
	require.Equal(t, _proxySchema.Keys(), keys(props(raw)["proxy"]), "proxy")
}

func TestJSONSchema_AcceptsTheFixturesTheDecoderAccepts(t *testing.T) {
	// An editor must not mark a working mock file as wrong.
	schema, _ := loadJSONSchema(t)
	m := fileServer(t, Setup())

	files, err := filepath.Glob("testdata/mocks/*.*")
	require.NoError(t, err)

	checked := 0

	for _, file := range files {
		if filepath.Base(file) == "items.json" { // a body_file, not a mock
			continue
		}

		_, err := m.Mock(FromFile(file))
		require.NoError(t, err, file)

		format, _ := config.Format(filepath.Ext(file))
		data, err := os.ReadFile(file)
		require.NoError(t, err)

		doc, err := config.Parse(data, format)
		require.NoError(t, err)

		b, err := json.Marshal(doc)
		require.NoError(t, err)

		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		require.NoError(t, err)
		require.NoError(t, schema.Validate(v), file)

		checked++
	}

	require.Equal(t, 5, checked)
}

func TestJSONSchema_RejectsWhatTheDecoderRejects(t *testing.T) {
	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent())

	for name, doc := range map[string]string{
		"no method":            `{"request": {}, "response": {}}`,
		"no answer":            `{"request": {"method": "Greetings"}}`,
		"response and bidi":    `{"request": {"method": "Chat"}, "response": {}, "bidi": {}}`,
		"response and proxy":   `{"request": {"method": "Greetings"}, "response": {}, "proxy": {"target": "localhost:1"}}`,
		"a proxy to nowhere":   `{"request": {"method": "Greetings"}, "proxy": {}}`,
		"message and messages": `{"request": {"method": "Greetings"}, "response": {"message": {}, "messages": []}}`,
		"an unknown key":       `{"request": {"method": "Greetings"}, "response": {"body": "x"}}`,
		"a code out of range":  `{"request": {"method": "Greetings"}, "response": {"status": {"code": 17}}}`,
	} {
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(doc)))
		require.NoError(t, err)
		require.Error(t, schema.Validate(v), name)

		_, err = FromBytes([]byte(doc), "json").Build(m)
		require.Error(t, err, "the decoder rejects it too: %s", name)
	}
}
