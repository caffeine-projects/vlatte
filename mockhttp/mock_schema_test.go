package mockhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// decodeDoc builds a YAML mock document for app, without the template pass, as the admin API does.
func decodeDoc(app *Server, doc string) (*Mock, error) {
	return mockDoc{origin: "doc", format: "yaml", data: []byte(doc)}.build(app)
}

// mockProblems returns the problems of a mock document's error, failing unless it is a mock *config.Error.
func mockProblems(t require.TestingT, err error) []config.Problem {
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "want a *config.Error, got %v", err)
	require.Equal(t, "mock", cfgErr.Scope)

	return cfgErr.Problems
}

// want is the part of a problem a rule fixes: its code, its key, and words its message and hint must hold.
type want struct {
	code    string
	key     string
	message string
	hint    string
}

func requireProblem(t *testing.T, problems []config.Problem, w want) {
	t.Helper()

	i := slices.IndexFunc(problems, func(p config.Problem) bool { return p.Code == w.code && p.Key == w.key })
	require.GreaterOrEqual(t, i, 0, "no %s problem for %q in %v", w.code, w.key, problems)

	p := problems[i]
	require.Equal(t, "doc", p.Origin)
	require.Contains(t, p.Message, w.message)
	require.Contains(t, p.Hint, w.hint)
}

func TestMockSchema_Rules(t *testing.T) {
	// One case per rule of the mock file format. Each names the problem's code and key, so the user can find it,
	// and the words that say what to do.
	m := MustNew(Setup().Silent())

	const ok = "request: {path: /x}\n"

	tests := []struct {
		name string
		doc  string
		want []want
	}{
		{"id with a slash", ok + "id: a/b\n", []want{{config.CodeInvalidValue, "id", "an id holds only letters", ""}}},
		{"id as a number", ok + "id: 123\n", []want{{config.CodeInvalidValue, "id", "id must be a string, not a number", `write it in quotes: "123"`}}},
		{"name as a number", ok + "name: 123\n", []want{{config.CodeInvalidValue, "name", "name must be a string, not a number", `write it in quotes: "123"`}}},
		{"priority as text", ok + "priority: high\n", []want{{config.CodeInvalidValue, "priority", "priority must be an integer, not a string", ""}}},
		{"priority in quotes", ok + "priority: '5'\n", []want{{config.CodeInvalidValue, "priority", "priority must be an integer, not a string", "write it without quotes: 5"}}},
		{"enabled as text", ok + "enabled: maybe\n", []want{{config.CodeInvalidValue, "enabled", `enabled must be true or false, not "maybe"`, ""}}},
		{"enabled as a number", ok + "enabled: 2\n", []want{{config.CodeInvalidValue, "enabled", "enabled must be true or false, not 2", ""}}},
		{"negative delay", ok + "delay: -5\n", []want{{config.CodeInvalidValue, "delay", "delay must be 0 or more, not -5", ""}}},
		{"fractional delay", ok + "delay: 1.5\n", []want{{config.CodeInvalidValue, "delay", "delay must be whole milliseconds or a Go duration, not 1.5", `such as "1.5ms"`}}},
		{"delay milliseconds in quotes", ok + "delay: '1500'\n", []want{{config.CodeInvalidValue, "delay", `delay must be milliseconds or a Go duration, not "1500"`, "write 1500 without quotes"}}},
		{"signed delay", ok + "delay: +1s\n", []want{{config.CodeInvalidValue, "delay", `delay must be milliseconds or a Go duration, not "+1s"`, "such as 1500 or 1.5s"}}},
		{"repeat below one", ok + "repeat: 0\n", []want{{config.CodeInvalidValue, "repeat", "expected 1 or more", ""}}},
		{"scenario without a name", ok + "scenario: {required_state: a}\n", []want{{config.CodeMissingValue, "scenario.name", "scenario.name is not", "name the scenario"}}},
		{"scenario that isn't an object", ok + "scenario: 5\n", []want{{config.CodeInvalidValue, "scenario", "must be an object", "its keys are name, required_state, new_state"}}},
		{"on_complete as an object", ok + "on_complete: {name: x}\n", []want{{config.CodeInvalidValue, "on_complete", "must be a list, not an object", ""}}},
		{"on_complete item that isn't an object", ok + "on_complete: [{name: x}, true]\n", []want{{config.CodeInvalidValue, "on_complete[1]", "must be an object, not a bool", ""}}},
		{"on_complete item without a name", ok + "on_complete: [{parameters: {}}]\n", []want{{config.CodeMissingValue, "on_complete[0].name", "is required", "OnCompleteListener"}}},
		{"on_complete item with a typo", ok + "on_complete: [{nmae: x}]\n", []want{{config.CodeUnknownKey, "on_complete[0].nmae", "not a known setting", "did you mean on_complete[0].name?"}}},
		{"no request", "response: {status: 200}\n", []want{{config.CodeMissingValue, "request", "sets no matcher", `path: "*"`}}},
		{"empty request", "request: {}\n", []want{{config.CodeMissingValue, "request", "sets no matcher", ""}}},
		{"method as a number", "request: {method: 5}\n", []want{{config.CodeInvalidValue, "request.method", "must be a string or a matcher list", `write it in quotes: "5"`}}},
		{"empty method in a list", "request: {method: 'GET,,POST'}\n", []want{{config.CodeInvalidValue, "request.method", "holds an empty method", "separate methods with commas"}}},
		{"empty matcher list", "request: {method: []}\n", []want{{config.CodeInvalidValue, "request.method", "is an empty list", "name a matcher"}}},
		{"unknown matcher", "request: {method: [nope]}\n", []want{{config.CodeInvalidValue, "request.method", "unknown matcher", ""}}},
		{"bad url", "request: {url: 'http://a b'}\n", []want{{config.CodeInvalidValue, "request.url", "is not a URL", ""}}},
		{"url_match as a list", "request: {url_match: [equal, x]}\n", []want{{config.CodeInvalidValue, "request.url_match", "request.url_match must be a string, not a list", ""}}},
		{"url_match that doesn't compile", "request: {url_match: '('}\n", []want{{config.CodeInvalidValue, "request.url_match", `request.url_match must be a regular expression, not "("`, "error parsing regexp"}}},
		{"url and path", "request: {url: /x, path: /y}\n", []want{{config.CodeConflict, "request.path", "request.path is set with request.url", "keep one of them"}}},
		{"url, url_match and path", "request: {url: /x, url_match: x, path: /y}\n", []want{
			{config.CodeConflict, "request.url_match", "is set with request.url", ""},
			{config.CodeConflict, "request.path", "is set with request.url", ""},
		}},
		{"query value as a number", "request: {query: {page: 1}}\n", []want{{config.CodeInvalidValue, "request.query.page", "must be a string or a matcher list", `write it in quotes: "1"`}}},
		{"form value as a bool", "request: {form_url_encoded: {active: true}}\n", []want{{config.CodeInvalidValue, "request.form_url_encoded.active", "not a bool", `write it in quotes: "true"`}}},
		{"header map as a list", "request: {header: [a]}\n", []want{{config.CodeInvalidValue, "request.header", "must be an object, not a list", ""}}},
		// Found by TestPropMockFile_ServesItsModel: no request could match both, so the mock never matched.
		{"two names of one header", "request: {header: {X-Id: a, x-id: b}}\n", []want{{config.CodeConflict, "request.header.x-id", "name the same header", "header names ignore case"}}},
		{"body as an object", "request: {body: {a: 1}}\n", []want{{config.CodeInvalidValue, "request.body", "must be a string or a matcher list", ""}}},
		{"matcher without its argument", "request: {header: {X-Id: [gt]}}\n", []want{{config.CodeInvalidValue, "request.header.X-Id", "gt", ""}}},
		{"multi-matcher entry that isn't a matcher", "request: {body: [all, [true]]}\n", []want{{config.CodeInvalidValue, "request.body", "a matcher is a name or a definition", ""}}},
		{"unknown top-level key", ok + "requset: {}\n", []want{{config.CodeUnknownKey, "requset", "not a known setting", "did you mean request?"}}},
		{"dotted key", "request.path: /x\nrequest: {method: GET}\n", []want{{config.CodeUnknownKey, "request.path", "written as a dotted key", "nest it"}}},
		{"response and proxy", ok + "response: {status: 200}\nproxy: {target: 'http://a'}\n", []want{{config.CodeConflict, "proxy", "proxy is set with response", "keep one of them"}}},
		{"response that isn't an object", ok + "response: 5\n", []want{{config.CodeInvalidValue, "response", "must be an object, not a number", ""}}},
		{"response key with a typo", ok + "response: {stauts: 201}\n", []want{{config.CodeUnknownKey, "response.stauts", "not a known setting", "did you mean response.status?"}}},
		{"response key with no near match", ok + "response: {colour: red}\n", []want{{config.CodeUnknownKey, "response.colour", "", "the keys under response are status, header"}}},
		{"status out of range", ok + "response: {status: 42}\n", []want{{config.CodeInvalidValue, "response.status", "from 100 to 599", ""}}},
		{"header value as a number", ok + "response: {header: {X-Count: 10}}\n", []want{{config.CodeInvalidValue, "response.header.X-Count", "must be a string or a list of strings", `write it in quotes: "10"`}}},
		{"header list holding a number", ok + "response: {header: {X: [a, 1]}}\n", []want{{config.CodeInvalidValue, "response.header.X[1]", "response.header.X[1] must be a string, not a number", `write it in quotes: "1"`}}},
		{"trailer value as a list of numbers", ok + "response: {trailer: {x: [1]}}\n", []want{{config.CodeInvalidValue, "response.trailer.x[0]", "must be a string, not a number", ""}}},
		{"null body", ok + "response: {body: null}\n", []want{{config.CodeInvalidValue, "response.body", "is null", `write "" for an empty body`}}},
		{"body and body_file", ok + "response: {body: x, body_file: y.txt}\n", []want{{config.CodeConflict, "response.body_file", "response.body_file is set with response.body", ""}}},
		{"empty body_file", ok + "response: {body_file: ''}\n", []want{{config.CodeInvalidValue, "response.body_file", "expected a file name", ""}}},
		{"template data as a number", ok + "response: {template: {data: 5}}\n", []want{{config.CodeInvalidValue, "response.template.data", "must be an object or a list", ""}}},
		{"template that isn't an object", ok + "response: {template: 5}\n", []want{{config.CodeInvalidValue, "response.template", "must be an object", "its keys are enabled, data"}}},
		{"unsupported encoding", ok + "response: {encoding: br}\n", []want{{config.CodeInvalidValue, "response.encoding", `response.encoding must be gzip, not "br"`, ""}}},
		{"encoding in capitals", ok + "response: {encoding: GZIP}\n", []want{{config.CodeInvalidValue, "response.encoding", `response.encoding must be gzip, not "GZIP"`, ""}}},
		{"sequence without responses", ok + "response_sequence: {}\n", []want{{config.CodeMissingValue, "response_sequence.responses", "is required", ""}}},
		{"sequence with no responses", ok + "response_sequence: {responses: []}\n", []want{{config.CodeInvalidValue, "response_sequence.responses", "is empty", "add at least one reply"}}},
		{"sequence reply with a bad status", ok + "response_sequence: {responses: [{status: 200}, {status: 1}]}\n", []want{{config.CodeInvalidValue, "response_sequence.responses[1].status", "from 100 to 599", ""}}},
		{"after_ended with a typo", ok + "response_sequence: {responses: [{}], after_ended: {bdy: x}}\n", []want{{config.CodeUnknownKey, "response_sequence.after_ended.bdy", "", "did you mean response_sequence.after_ended.body?"}}},
		{"sequence that isn't an object", ok + "response_sequence: 5\n", []want{{config.CodeInvalidValue, "response_sequence", "must be an object", ""}}},
		{"random seed as text", ok + "response_random: {responses: [{}], seed: x}\n", []want{{config.CodeInvalidValue, "response_random.seed", "response_random.seed must be an integer, not a string", ""}}},
		{"random without responses", ok + "response_random: {seed: 1}\n", []want{{config.CodeMissingValue, "response_random.responses", "is required", ""}}},
		{"proxy without a target", ok + "proxy: {timeout: 10}\n", []want{{config.CodeMissingValue, "proxy.target", "is required", ""}}},
		{"proxy target that isn't absolute", ok + "proxy: {target: /x}\n", []want{{config.CodeInvalidValue, "proxy.target", "expected an absolute URL", ""}}},
		{"negative proxy timeout", ok + "proxy: {target: 'http://a', timeout: -1}\n", []want{{config.CodeInvalidValue, "proxy.timeout", "proxy.timeout must be 0 or more, not -1", ""}}},
		{"remove_headers as an object", ok + "proxy: {target: 'http://a', remove_headers: {a: 1}}\n", []want{{config.CodeInvalidValue, "proxy.remove_headers", "proxy.remove_headers must be a list of strings, not an object", ""}}},
		{"remove_headers as text", ok + "proxy: {target: 'http://a', remove_headers: 'a, b'}\n", []want{{config.CodeInvalidValue, "proxy.remove_headers", "must be a list of strings, not a string", `write a list: ["a", "b"]`}}},
		{"target as a number", ok + "proxy: {target: 5}\n", []want{{config.CodeInvalidValue, "proxy.target", "proxy.target must be a string, not a number", `write it in quotes: "5"`}}},
		{"ext as a list", ok + "ext: [1]\n", []want{{config.CodeInvalidValue, "ext", "must be an object, not a list", ""}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := decodeDoc(m, tc.doc)
			require.Nil(t, mock)

			problems := mockProblems(t, err)
			require.Len(t, problems, len(tc.want), "%v", problems)

			for _, w := range tc.want {
				requireProblem(t, problems, w)
			}
		})
	}
}

func TestMockSchema_EveryProblemAtOnce(t *testing.T) {
	// A file is fixed in one pass: every problem is listed, not only the first.
	_, err := decodeDoc(MustNew(Setup().Silent()), `
requset: {}
request: {method: 5, path: /x, url: /y}
response: {status: 42, stauts: 1}
proxy: {target: 'http://a'}
`)

	got := make([]string, 0, 6)
	for _, p := range mockProblems(t, err) {
		got = append(got, p.Code+" "+p.Key)
	}

	require.ElementsMatch(t, []string{
		"unknown_key requset",
		"invalid_value request.method",
		"conflict request.path",
		"invalid_value response.status",
		"unknown_key response.stauts",
		"conflict proxy",
	}, got)
}

func TestMockSchema_ErrorText(t *testing.T) {
	// The whole error, as users read it: one line per problem, sorted, with the origin and the fix.
	_, err := FromBytes([]byte("request: {query: {page: 1}}\nresponse: {stauts: 201, body: x, body_file: y}\nrepeat: 0\n"), "yaml").
		Build(MustNew(Setup().Silent()))

	require.EqualError(t, err, `mock: 4 problems
  bytes: invalid_value: repeat="0": expected 1 or more
  bytes: invalid_value: request.query.page must be a string or a matcher list, such as [equal, x], not a number; write it in quotes: "1"
  bytes: conflict: response.body_file is set with response.body; keep one of them
  bytes: unknown_key: response.stauts is not a known setting; did you mean response.status?`)
}

func TestMockSchema_ReadProblems(t *testing.T) {
	m := MustNew(Setup().Silent())

	t.Run("syntax error points to the line, without noise from checks", func(t *testing.T) {
		_, err := mockDoc{origin: "doc", format: "json", data: []byte("{\n  \"request\": \n}")}.build(m)

		problems := mockProblems(t, err)
		require.Len(t, problems, 1)
		require.Equal(t, config.CodeParseError, problems[0].Code)
		require.Equal(t, "doc:3:1", problems[0].Origin)
	})

	t.Run("unsupported format", func(t *testing.T) {
		_, err := FromBytes([]byte("a = 1"), "toml").Build(m)
		require.Equal(t, config.CodeUnsupportedFormat, mockProblems(t, err)[0].Code)
	})

	t.Run("a file with another extension", func(t *testing.T) {
		_, err := FromFile("testdata/loader/02.txt").Build(m)
		problems := mockProblems(t, err)

		require.Equal(t, config.CodeUnsupportedFormat, problems[0].Code)
		require.Equal(t, "file testdata/loader/02.txt", problems[0].Origin)
	})

	t.Run("a missing file", func(t *testing.T) {
		_, err := FromFile("testdata/missing.mock.yaml").Build(m)
		problems := mockProblems(t, err)

		require.Equal(t, config.CodeFileNotFound, problems[0].Code)
		require.Contains(t, problems[0].Hint, "relative paths start at")
	})

	t.Run("template error says how to escape reply templates", func(t *testing.T) {
		_, err := FromBytes([]byte("request: {path: /x}\nresponse: {body: '{{ .Request.Method }}'}\n"), "yaml").Build(m)
		problems := mockProblems(t, err)

		require.Equal(t, config.CodeParseError, problems[0].Code)
		require.Contains(t, problems[0].Hint, "{{`{{ .Request.Method }}`}}")
	})

	t.Run("an unregistered listener", func(t *testing.T) {
		_, err := decodeDoc(m, "request: {path: /x}\non_complete: [{name: nobody}]\n")
		require.Contains(t, mockProblems(t, err)[0].Message, "on_complete listener nobody at index 0 is not registered")
	})
}

type capturingHandler struct {
	got map[string]any
	err error
}

func (h *capturingHandler) Handle(fields map[string]any, _ *Builder) error {
	h.got = fields
	return h.err
}

func TestMockFile_ExtKeepsKeysAndNumbers(t *testing.T) {
	// B6: ext reaches handlers with its keys as written, and numbers in one form for JSON and YAML.
	for _, format := range []string{"json", "yaml"} {
		h := &capturingHandler{}
		m := MustNew(Setup().Silent().MockFileHandlers(h))

		doc := map[string]string{
			"json": `{"request": {"path": "/x"}, "ext": {"userId": 7, "Nested": {"camelCase": [1.5, "2024-01-01"]}}}`,
			"yaml": "request: {path: /x}\next: {userId: 7, Nested: {camelCase: [1.5, 2024-01-01]}}\n",
		}[format]

		_, err := FromBytes([]byte(doc), format).Build(m)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"userId": int64(7), "Nested": map[string]any{"camelCase": []any{1.5, "2024-01-01"}}}, h.got, format)
	}
}

func TestMockFile_ExtHandlerErrors(t *testing.T) {
	h := &capturingHandler{err: errors.New("unknown flag")}
	m := MustNew(Setup().Silent().MockFileHandlers(h))

	_, err := FromBytes([]byte("request: {path: /x}\next: {a: 1}\n"), "yaml").Build(m)

	problems := mockProblems(t, err)
	require.Equal(t, "ext", problems[0].Key)
	require.Equal(t, "mock file handler 0: unknown flag", problems[0].Message)
}

func TestMockFile_HonorsID(t *testing.T) {
	// F15: a file names its mock, so it can be found, replaced or deleted by that name.
	m := MustNew(Setup().Silent())

	mock, err := decodeDoc(m, "id: users-get\nrequest: {path: /users}\n")
	require.NoError(t, err)
	require.Equal(t, "users-get", mock.ID())

	_, err = m.Mock(FromBytes([]byte("id: same\nrequest: {path: /a}\n"), "yaml"))
	require.NoError(t, err)

	_, err = m.Mock(FromBytes([]byte("id: same\nrequest: {path: /b}\n"), "yaml"))
	require.ErrorIs(t, err, vlatte.ErrDuplicateID)
}

func TestMockFile_BuildIsRepeatable(t *testing.T) {
	// F2: building twice gives two mocks, not one mock with doubled matchers.
	m := MustNew(Setup().Silent())
	b := FromFile("testdata/loader/01.mock.json")

	first, err := b.Build(m)
	require.NoError(t, err)
	second, err := b.Build(m)
	require.NoError(t, err)

	require.NotSame(t, first, second)
	require.NotEqual(t, first.ID(), second.ID())
	require.Len(t, second.expectations, len(first.expectations))
	require.Equal(t, strings.Count(describe(t, first), "\""), strings.Count(describe(t, second), "\""))
}

func describe(t require.TestingT, m *Mock) string {
	b, err := m.MarshalJSON()
	require.NoError(t, err)

	return string(b)
}

func TestMockFile_Regressions(t *testing.T) {
	// Bugs of the viper decoder, by the IDs in docs/.ai/followups-plan.md.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	get := func(t *testing.T, doc, path string) (*http.Response, string) {
		t.Helper()

		scope, err := m.Mock(FromBytes([]byte(doc), "yaml"))
		require.NoError(t, err)
		t.Cleanup(func() { scope.Clear(context.Background()) })

		// The path may hold a query, which URL would escape.
		res, err := http.Get(m.URL() + path)
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res, string(b)
	}

	t.Run("B5: int, list and object bodies are served as JSON", func(t *testing.T) {
		for path, body := range map[string]string{"/int": "42", "/list": "[1,2]", "/object": `{"a":[true,1.5]}`} {
			_, got := get(t, "request: {path: "+path+"}\nresponse: {body: "+body+"}\n", path)
			require.JSONEq(t, body, got, path)
		}
	})

	t.Run("B6: query names keep their case", func(t *testing.T) {
		res, _ := get(t, "request: {path: /case, query: {userId: '7'}}\n", "/case?userId=7")
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err := http.Get(m.URL() + "/case?userid=7")
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, StatusNoMatch, res.StatusCode)
	})

	t.Run("C2: proxy.timeout numbers are milliseconds", func(t *testing.T) {
		mock, err := decodeDoc(m, "request: {path: /x}\nproxy: {target: 'http://localhost', timeout: 1500}\n")
		require.NoError(t, err)

		proxy, ok := mock.Reply.(*ProxyReply)
		require.True(t, ok)
		require.Equal(t, 1500*time.Millisecond, proxy.timeout)
	})

	t.Run("F9: ssl_verify true verifies the target", func(t *testing.T) {
		target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("target")) }))
		t.Cleanup(target.Close)

		// A reply that fails answers with the no-match status and the error.
		res, body := get(t, "request: {path: /verify}\nproxy: {target: '"+target.URL+"', ssl_verify: true}\n", "/verify")
		require.Equal(t, StatusNoMatch, res.StatusCode)
		require.Contains(t, body, "failed to verify certificate")

		res, body = get(t, "request: {path: /skip}\nproxy: {target: '"+target.URL+"', ssl_verify: false}\n", "/skip")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "target", body)
	})
}

func TestMockFile_FormFieldsComeFromTheBody(t *testing.T) {
	// Found by TestPropMockFile_ServesItsModel at 10000 checks: form fields were read from http.Request.Form, which
	// also holds the query, so a query parameter could stand in for a form field that the body didn't have.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request:\n  method: POST\n  path: /form\n  form_url_encoded: {H: [present], A: '-'}\n"), "yaml"))

	post := func(query, form string) int {
		res, err := http.Post(m.URL()+"/form?"+query, MIMEFormURLEncoded, strings.NewReader(form))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	require.Equal(t, http.StatusOK, post("", "H=x&A=-"))
	require.Equal(t, StatusNoMatch, post("H=x", "A=-"), "H is in the query, not in the form")
	require.Equal(t, StatusNoMatch, post("A=-", "H=x"), "A is in the query, not in the form")
}

func TestMockFile_ProxyKeys(t *testing.T) {
	// Every proxy key reaches the forwarded request or the response.
	var forwarded *http.Request

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r
		w.Write([]byte("target"))
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `
request: {path: /api/users.json}
proxy:
  target: ` + target.URL + `
  forward_header: {X-Fwd: a}
  header: {X-Res: [b, c]}
  remove_headers: [X-Drop]
  trim_prefix: /api
  trim_suffix: .json
  timeout: 1500
  ssl_verify: false
  no_follow: true
`
	scope, err := m.Mock(FromBytes([]byte(doc), "yaml"))
	require.NoError(t, err)

	proxy, ok := scope.Mocks()[0].Reply.(*ProxyReply)
	require.True(t, ok)
	require.Equal(t, 1500*time.Millisecond, proxy.timeout)
	require.True(t, proxy.noFollow)

	req, err := http.NewRequest(http.MethodGet, m.URL("/api/users.json"), nil)
	require.NoError(t, err)
	req.Header.Set("X-Drop", "secret")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"b", "c"}, res.Header.Values("X-Res"))
	require.Equal(t, "/users", forwarded.URL.Path)
	require.Equal(t, "a", forwarded.Header.Get("X-Fwd"))
	require.Empty(t, forwarded.Header.Get("X-Drop"))
}

func TestMockSchema_MoreReadProblems(t *testing.T) {
	m := MustNew(Setup().Silent())

	t.Run("a directory named like a mock file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "x.mock.yaml")
		require.NoError(t, os.Mkdir(dir, 0o700))

		_, err := FromFile(dir).Build(m)
		require.Equal(t, config.CodeReadError, mockProblems(t, err)[0].Code)
	})

	t.Run("a template that doesn't parse", func(t *testing.T) {
		_, err := FromBytes([]byte("request: {path: '{{ .URL'}\n"), "yaml").Build(m)

		problems := mockProblems(t, err)
		require.Equal(t, config.CodeParseError, problems[0].Code)
		require.Contains(t, problems[0].Message, "template")
	})

	t.Run("a missing absolute path needs no hint", func(t *testing.T) {
		_, err := FromFile(filepath.Join(t.TempDir(), "missing.mock.yaml")).Build(m)

		problems := mockProblems(t, err)
		require.Equal(t, config.CodeFileNotFound, problems[0].Code)
		require.Empty(t, problems[0].Hint)
	})
}
