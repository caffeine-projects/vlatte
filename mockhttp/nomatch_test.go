package mockhttp

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
)

// noMatchServer starts a server with setup and one mock, users, that a GET of /users without Accept: application/json
// misses by its header.
func noMatchServer(t *testing.T, setup *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(setup.Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/users").ID("users").Name("list users").Header("X-Tenant", match.Equal("a")).Reply(OK()))

	return m
}

func TestNoMatch_AnswersInTheFormatAcceptPrefers(t *testing.T) {
	// The client that most often misses a mock is the application under test: one that asks for JSON must get JSON it
	// can decode, so the reason shows in its own error, and problem details only when it prefers them to JSON. A
	// browser, curl or a client without Accept gets the Markdown document as text, which reads as well raw.
	tests := []struct {
		accept string
		want   string
	}{
		{accept: "", want: MIMETextPlainCharsetUTF8},
		{accept: "*/*", want: MIMETextPlainCharsetUTF8},
		{accept: "text/*", want: MIMETextPlainCharsetUTF8},
		{accept: "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", want: MIMETextPlainCharsetUTF8},
		{accept: "application/xml", want: MIMETextPlainCharsetUTF8},
		{accept: "text/markdown", want: MIMETextMarkdownCharsetUTF8},
		{accept: "text/markdown, text/plain", want: MIMETextMarkdownCharsetUTF8},
		{accept: "text/plain, text/markdown", want: MIMETextPlainCharsetUTF8},
		{accept: "application/json", want: MIMEApplicationJSON},
		{accept: "application/json, text/plain, */*", want: MIMEApplicationJSON},
		{accept: "text/plain, application/json", want: MIMETextPlainCharsetUTF8},
		{accept: "*/*, application/json", want: MIMEApplicationJSON},
		{accept: "application/*", want: MIMEApplicationJSON},
		{accept: "application/vnd.api+json", want: MIMEApplicationJSON},
		{accept: "application/vnd.api+json;q=0.5, text/plain", want: MIMETextPlainCharsetUTF8},
		{accept: "application/json;q=0, application/vnd.api+json", want: MIMETextPlainCharsetUTF8},
		{accept: "application/vnd.api+json;q=0.1, application/*", want: MIMEApplicationJSON},
		{accept: "application/vnd.api+json, text/plain, application/json;q=0.5", want: MIMEApplicationJSON},
		{accept: "application/json;q=0, */*", want: MIMETextPlainCharsetUTF8},
		{accept: "application/problem+json", want: MIMEApplicationProblemJSON},
		{accept: "application/problem+json, */*", want: MIMEApplicationProblemJSON},
		{accept: "application/*;q=0.5, application/problem+json;q=0.5", want: MIMEApplicationProblemJSON},
		{accept: "application/problem+json, application/json;q=0.9", want: MIMEApplicationProblemJSON},
		{accept: "application/problem+json, application/json", want: MIMEApplicationJSON},
		{accept: "application/json, application/problem+json", want: MIMEApplicationJSON},
		{accept: "application/json;q=0.5, application/problem+json;q=0.5, text/plain;q=0.1", want: MIMEApplicationJSON},
	}

	m := noMatchServer(t, Setup())

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			header := http.Header{}
			if tt.accept != "" {
				header.Set(HeaderAccept, tt.accept)
			}

			res, _ := do(t, http.MethodGet, m.URL("/users"), header, nil)
			require.Equal(t, StatusNoMatch, res.StatusCode)
			require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
			require.Equal(t, tt.want, res.Header.Get(HeaderContentType))
			require.Equal(t, []string{HeaderAccept}, res.Header.Values(HeaderVary), "caches keep an answer per Accept")
		})
	}
}

func TestNoMatch_JSONIsProblemDetailsWithTheNearMisses(t *testing.T) {
	// A JSON client reads the answer as RFC 9457 problem details, the error shape client libraries know, and finds
	// the near misses in the shape the journal and the MCP tools give them.
	m := noMatchServer(t, Setup())

	for _, accept := range []string{MIMEApplicationJSON, MIMEApplicationProblemJSON} {
		t.Run(accept, func(t *testing.T) {
			res, body := do(t, http.MethodGet, m.URL("/users"), http.Header{HeaderAccept: {accept}}, nil)
			require.Equal(t, accept, res.Header.Get(HeaderContentType))

			want, err := json.Marshal(map[string]any{
				"type":       "https://caffeine-projects.github.io/vlatte/http/troubleshooting#near-misses",
				"title":      "Request did not match",
				"status":     StatusNoMatch,
				"detail":     "No mock matched GET " + m.URL("/users") + ".",
				"request_id": res.Header.Get(vlatte.HeaderRequestID),
				"request":    map[string]any{"method": http.MethodGet, "url": m.URL("/users")},
				"near_misses": []map[string]any{
					{"mock_id": "users", "name": "list users", "passed": 2, "total": 3, "failed": []string{"header X-Tenant"}},
				},
			})
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(body))
		})
	}
}

func TestNoMatch_JSONStaysValidWithInvalidUTF8(t *testing.T) {
	// A mock name or a raw query byte can be invalid UTF-8: the JSON client still gets JSON it can decode, with the
	// replacement character in its place, not an empty body labeled as JSON.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/a").ID("bad").Name("caf\xe9").Header("X", match.Equal("1")).Reply(OK()))

	_, body := do(t, http.MethodGet, m.URL("/a"), http.Header{HeaderAccept: {MIMEApplicationJSON}}, nil)

	var problem struct {
		NearMisses []vlatte.NearMiss `json:"near_misses"`
	}

	require.NoError(t, json.Unmarshal(body, &problem), string(body))
	require.Len(t, problem.NearMisses, 1)
	require.Equal(t, "caf\uFFFD", problem.NearMisses[0].Name)
}

func TestNoMatch_TheFormatSettingIgnoresAccept(t *testing.T) {
	// A team whose tools read one format sets it, and every client gets it, whatever it accepts: the answer varies by
	// nothing, so it says so to no cache.
	for name, setup := range map[string]*ConfigBuilder{
		"builder": Setup().NoMatchFormat(NoMatchMarkdown),
		"setting": Setup().ConfigSources(ConfigArgs([]string{"--no-match-format=markdown"})),
	} {
		t.Run(name, func(t *testing.T) {
			m := noMatchServer(t, setup)

			res, body := do(t, http.MethodGet, m.URL("/users"), http.Header{HeaderAccept: {MIMEApplicationJSON}}, nil)
			require.Equal(t, MIMETextMarkdownCharsetUTF8, res.Header.Get(HeaderContentType))
			require.Empty(t, res.Header.Values(HeaderVary))
			require.True(t, strings.HasPrefix(string(body), "# Request did not match\n"), string(body))
		})
	}
}

func TestNoMatch_TellsWhereTheJournalHasTheRequest(t *testing.T) {
	// An agent or a person that reads the answer can fetch the whole request, its headers and body included, from the
	// link the answer gives: the admin API listens on its own address.
	m := noMatchServer(t, Setup().Journal("").Admin(admin.MustNew()))

	res, body := do(t, http.MethodGet, m.URL("/users"), nil, nil)
	id := res.Header.Get(vlatte.HeaderRequestID)
	link := m.AdminURL() + "/requests/" + id
	require.Contains(t, string(body), "\n- See the request in the journal: `GET "+link+"`\n")

	entry, _ := do(t, http.MethodGet, link, nil, nil)
	require.Equal(t, http.StatusOK, entry.StatusCode)
}

func TestNoMatch_PointsToNoJournalItDoesNotKeep(t *testing.T) {
	// A link to a journal the server doesn't keep would send the reader to a 404.
	m := noMatchServer(t, Setup().Admin(admin.MustNew()))

	_, body := do(t, http.MethodGet, m.URL("/users"), nil, nil)
	require.NotContains(t, string(body), "journal")
}

func TestNoMatch_KeepsTheVaryOfCORS(t *testing.T) {
	// A browser's cache keys a CORS answer by Origin: negotiating the format adds Accept to Vary, and keeps Origin.
	m := noMatchServer(t, Setup().CORS())

	res, _ := do(t, http.MethodGet, m.URL("/users"), http.Header{"Origin": {"https://example.org"}}, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.ElementsMatch(t, []string{HeaderOrigin, HeaderAccept}, res.Header.Values(HeaderVary))
}

func TestNoMatch_ValuesCannotChangeTheDocument(t *testing.T) {
	// The request and the mocks are the client's and the mock author's: a backtick, a # or a line break in a name, a
	// path or a matcher's error stays in its code span, on its line, so no value can add a heading an agent would read
	// as the server's own words.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/a").ID("xy").Name("``# fake\n## heading`").Header("X", match.Equal("1")).Reply(OK()))

	_, body := do(t, http.MethodGet, m.URL("/a"), nil, nil)
	require.Contains(t, string(body), "\n1. `xy` (``` ``# fake ## heading` ```): 2 of 3 parts matched; differs in `header X`\n")

	headings := 0
	for line := range strings.Lines(string(body)) {
		if strings.HasPrefix(line, "#") {
			headings++
		}
	}

	require.Equal(t, 3, headings, "the title, the closest mocks and the next steps")
}

func TestWriteCodeSpan_HoldsAnyValueWhole(t *testing.T) {
	// Whatever a value holds, the span that writes it is one line, and reads back, as CommonMark reads a code span, as
	// the value with its line breaks as spaces.
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringOfN(rapid.SampledFrom([]rune{'a', '`', ' ', '#', '\n', '\r', '*'}), 1, 12, -1).Draw(t, "s")
		want := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)

		var buf bytes.Buffer
		writeCodeSpan(&buf, s)

		require.NotContains(t, buf.String(), "\n")
		require.NotContains(t, buf.String(), "\r")
		require.Equal(t, want, readCodeSpan(t, buf.String()))
	})
}

func TestNoMatch_ACustomRendererAnswers(t *testing.T) {
	// A team whose clients expect their own error shape answers in it, with the mocks that came closest, as the
	// journal records them. The VLatte-Error header stays, so tests still tell the answer from a mock's.
	var got []vlatte.NearMiss

	renderer := noMatchFunc(func(w http.ResponseWriter, nm *NoMatch) {
		got = nm.NearMisses
		w.Header().Set(HeaderContentType, MIMEApplicationJSON)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"NOT_FOUND"}`))
	})

	m := noMatchServer(t, Setup().Journal("").NoMatchRenderer(renderer))

	res, body := do(t, http.MethodGet, m.URL("/users"), nil, nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
	require.JSONEq(t, `{"code":"NOT_FOUND"}`, string(body))

	entry := only(t, m, journal.Query{Path: match.Equal("/users")})
	require.Equal(t, journal.OutcomeUnmatched, entry.Outcome)
	require.Equal(t, http.StatusNotFound, entry.Status)
	require.Equal(t, got, entry.NearMisses)
	require.Len(t, got, 1)
}

func TestNoMatch_ACustomRendererCanHandOverToTheDefault(t *testing.T) {
	// A renderer that answers only some requests hands the others to the default one, which negotiates as ever.
	renderer := noMatchFunc(func(w http.ResponseWriter, nm *NoMatch) {
		DefaultNoMatchRenderer(NoMatchAuto).RenderNoMatch(w, nm)
	})

	m := noMatchServer(t, Setup().NoMatchRenderer(renderer))

	res, _ := do(t, http.MethodGet, m.URL("/users"), http.Header{HeaderAccept: {MIMEApplicationJSON}}, nil)
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Equal(t, MIMEApplicationJSON, res.Header.Get(HeaderContentType))
}

func TestNoMatch_ARendererThatWritesNothingAnswersTheStatus(t *testing.T) {
	// net/http answers 200 to a handler that writes nothing: a renderer that forgets to must not pass a missed mock off
	// as a success.
	m := noMatchServer(t, Setup().MockNotFoundStatusCode(http.StatusNotFound).
		NoMatchRenderer(noMatchFunc(func(http.ResponseWriter, *NoMatch) {})))

	res, body := do(t, http.MethodGet, m.URL("/users"), nil, nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(HeaderVlatteError))
	require.Equal(t, "nosniff", res.Header.Get(HeaderXContentTypeOptions), "a custom renderer's answer isn't sniffed either")
	require.Empty(t, body)
}

func TestNoMatch_AFormatAndARendererDoNotGoTogether(t *testing.T) {
	// The format configures the default renderer: with a renderer of its own, a server would ignore it without a word.
	renderer := DefaultNoMatchRenderer(NoMatchText)

	_, err := New(Setup().Silent().NoMatchRenderer(renderer).NoMatchFormat(NoMatchJSON))
	require.ErrorIs(t, err, errNoMatchFormatWithRenderer)

	_, err = New(Setup().Silent().NoMatchRenderer(renderer).ConfigSources(ConfigArgs([]string{"--no-match-format=json"})))
	require.ErrorIs(t, err, errNoMatchFormatWithRenderer)

	m, err := New(Setup().Silent().NoMatchRenderer(renderer).NoMatchFormat(NoMatchAuto))
	require.NoError(t, err)
	m.Close()
}

func TestNoMatch_AFormatThatIsNotOneFails(t *testing.T) {
	// A format from code that names none fails the server, and the default renderer, rather than answer in a format
	// nobody picked.
	_, err := New(Setup().Silent().NoMatchFormat(NoMatchProblem + 1))
	require.EqualError(t, err, "server: config: no-match format 5 is not one of the NoMatch* formats")

	require.PanicsWithError(t, "mockhttp: no-match format -1 is not one of the NoMatch* formats", func() {
		DefaultNoMatchRenderer(-1)
	})
}

// noMatchFunc is a NoMatchRenderer of a function.
type noMatchFunc func(w http.ResponseWriter, nm *NoMatch)

func (f noMatchFunc) RenderNoMatch(w http.ResponseWriter, nm *NoMatch) { f(w, nm) }

// readCodeSpan reads span, a whole code span, as CommonMark does: the run of backticks that opens it closes it, and a
// space is stripped from each end of what they hold when both ends have one and it isn't only spaces.
func readCodeSpan(t require.TestingT, span string) string {
	n := len(span) - len(strings.TrimLeft(span, "`"))
	fence := strings.Repeat("`", n)

	require.True(t, strings.HasSuffix(span, fence) && len(span) >= 2*n, "a span closes with its fence: %q", span)

	content := span[n : len(span)-n]
	for run := range strings.FieldsFuncSeq(content, func(r rune) bool { return r != '`' }) {
		require.NotEqual(t, n, len(run), "no run of backticks in the span closes it: %q", span)
	}

	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.TrimSpace(content) != "" {
		content = content[1 : len(content)-1]
	}

	return content
}
