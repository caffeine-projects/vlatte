package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/match"
)

// journaledServer starts a server that keeps a journal in a temporary directory.
func journaledServer(t *testing.T, setup *ConfigBuilder) *Server {
	t.Helper()

	m := MustNew(setup.Silent().Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	return m
}

// only returns the one entry of the journal of m that q selects.
func only(t *testing.T, m *Server, q vlatte.JournalQuery) *vlatte.JournalEntry {
	t.Helper()

	entries, err := m.Requests(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	return entries[0]
}

func TestJournal_KeepsEachRequestOnceWhateverItsOutcome(t *testing.T) {
	// A test verifies the requests a server got, not only those a mock answered: each request is one entry, with
	// the outcome it reached first, whether a mock answered it, none did, another server did, serving it failed,
	// it panicked, or its client left, and the status the client got, which a reply that writes its own response
	// sends.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(upstream.Close)

	// Failures get a status of their own, so an entry tells them from a request no mock matched.
	m := journaledServer(t, Setup().ErrorStatusCode(599))
	proxied := journaledServer(t, Setup().ConfigSources(ConfigArgs([]string{"--proxy-all-target", upstream.URL})))

	m.MustMock(
		Getf("/matched").ID("matched").Reply(OK()),
		Getf("/failed").ID("failed").Reply(OK().BodyFile("missing.txt")),
		Getf("/panics").ID("panics").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { panic("boom") })),
		Getf("/slow").ID("slow").Delay(5*time.Second).Reply(OK()),
		Getf("/handler").ID("handler").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })),
		Getf("/silent").ID("silent").Reply(Handler(func(http.ResponseWriter, *http.Request) {})),
		Getf("/echo").ID("echo").Reply(Echo()),
		Getf("/late").ID("late").Reply(Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
			w.WriteHeader(http.StatusAccepted)
			return nil, errors.New("late failure")
		})),
		Getf("/aborts").ID("aborts").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
			panic(http.ErrAbortHandler)
		})),
		Getf("/exits").ID("exits").Reply(Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
			runtime.Goexit()
			return nil, nil
		})),
		Getf("/aborts-late").ID("aborts-late").Reply(Func(func(_ http.ResponseWriter, r *RequestValues) (*Response, error) {
			<-r.RawRequest.Context().Done()
			panic(http.ErrAbortHandler)
		})),
		Getf("/takes-over").ID("takes-over").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				conn.Close()
			}

			panic("taken")
		})),
	)

	tests := []struct {
		name    string
		server  *Server
		path    string
		outcome vlatte.Outcome
		status  int
		mockID  string
		message string
	}{
		{name: "matched", server: m, path: "/matched", outcome: vlatte.OutcomeMatched, status: http.StatusOK, mockID: "matched"},
		{name: "unmatched", server: m, path: "/nothing", outcome: vlatte.OutcomeUnmatched, status: StatusNoMatch},
		{name: "proxied", server: proxied, path: "/up", outcome: vlatte.OutcomeProxied, status: http.StatusAccepted},
		{name: "failed", server: m, path: "/failed", outcome: vlatte.OutcomeFailed, status: 599, mockID: "failed", message: "missing.txt"},
		{name: "panicked", server: m, path: "/panics", outcome: vlatte.OutcomeFailed, status: 599, mockID: "panics", message: "panicked: boom"},
		{name: "aborted", server: m, path: "/aborts", outcome: vlatte.OutcomeFailed, mockID: "aborts", message: "abort Handler"},
		{name: "exited", server: m, path: "/exits", outcome: vlatte.OutcomeFailed, mockID: "exits", message: "ended without an answer"},
		{name: "aborted once its client left", server: m, path: "/aborts-late", outcome: vlatte.OutcomeAbandoned, mockID: "aborts-late", message: "canceled"},
		{name: "panicked on a connection taken over", server: m, path: "/takes-over", outcome: vlatte.OutcomeFailed, mockID: "takes-over", message: "panicked: taken"},
		{name: "abandoned", server: m, path: "/slow", outcome: vlatte.OutcomeAbandoned, mockID: "slow", message: "left during the delay"},
		{name: "handler", server: m, path: "/handler", outcome: vlatte.OutcomeMatched, status: http.StatusCreated, mockID: "handler"},
		{name: "handler writing nothing", server: m, path: "/silent", outcome: vlatte.OutcomeMatched, status: http.StatusOK, mockID: "silent"},
		{name: "echo", server: m, path: "/echo", outcome: vlatte.OutcomeMatched, status: http.StatusOK, mockID: "echo"},
		{name: "failed after its status", server: m, path: "/late", outcome: vlatte.OutcomeFailed, status: http.StatusAccepted, mockID: "late", message: "late failure"},
	}

	// A client retries a request whose connection, one it reused, drops without an answer: each request goes on a
	// connection of its own, so the server gets it once.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, tt.server.URL(tt.path), nil)
			require.NoError(t, err)

			if res, err := client.Do(req); err == nil {
				res.Body.Close()
			}

			q := vlatte.JournalQuery{Path: match.Equal(tt.path)}

			// The client of a request that hangs leaves before the server ends it.
			require.Eventually(t, func() bool {
				n, err := tt.server.CountRequests(t.Context(), q)
				return err == nil && n > 0
			}, 5*time.Second, 10*time.Millisecond)

			e := only(t, tt.server, q)
			require.Equal(t, tt.outcome, e.Outcome)
			require.Equal(t, tt.status, e.Status)
			require.Equal(t, tt.mockID, e.MockID)
			require.Contains(t, e.Message, tt.message)
			require.Equal(t, Kind, e.Kind)
			require.Equal(t, http.MethodGet, e.Method)
			require.Equal(t, tt.server.URL(tt.path), e.Target)
			require.NotEmpty(t, e.ID)
		})
	}
}

func TestJournal_KeepsTheRequestAsItCame(t *testing.T) {
	// The entry is the request the client sent: what a callback, a mapper or a proxy does to it later isn't. Headers
	// the server redacts in its logs are redacted, and a body over the journal's limit is cut, and says so.
	m := journaledServer(t, Setup().RedactHeader("Authorization").ConfigSources(ConfigArgs([]string{"--journal-max-body-size", "4"})))

	m.MustMock(Postf("/a").Callback(func(_ context.Context, in vlatte.CallbackInput) error {
		in.(*CallbackInput).Request().RawRequest.Header.Set("X-Changed", "later")
		return nil
	}).Reply(OK()))

	res, _ := do(t, http.MethodPost, m.URL("/a?q=1"), http.Header{"Authorization": {"Bearer secret"}, "X-Mine": {"1"}}, []byte("123456"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	e := only(t, m, vlatte.JournalQuery{})
	require.Equal(t, "1", http.Header(e.Header).Get("X-Mine"))
	require.Empty(t, http.Header(e.Header).Get("X-Changed"))
	require.NotContains(t, http.Header(e.Header).Get("Authorization"), "secret")
	require.Equal(t, strings.TrimPrefix(m.URL(), "http://"), http.Header(e.Header).Get("Host"))
	require.Equal(t, "1234", string(e.Body))
	require.True(t, e.BodyTruncated)
	require.Equal(t, m.URL("/a?q=1"), e.Target)
	require.Equal(t, "/a", e.Path)
}

func TestJournal_UnmatchedEntriesNameTheClosestMocks(t *testing.T) {
	m := journaledServer(t, Setup())
	m.MustMock(Getf("/users").ID("users").Header("Accept", match.Equal("application/json")).Reply(OK()))

	_, _ = do(t, http.MethodGet, m.URL("/users"), nil, nil)

	e := only(t, m, vlatte.JournalQuery{Outcome: vlatte.OutcomeUnmatched})
	require.Equal(t, []vlatte.NearMiss{{MockID: "users", Passed: 2, Total: 3, Failed: []string{"header Accept"}}}, e.NearMisses)
}

func TestJournal_VerifiesWithRequestPatterns(t *testing.T) {
	// A test verifies with the request part of a mock, as WireMock's verify does, with all a mock can match: query
	// parameters, form fields, files of a multipart form, the parsed body, a predicate of the request.
	m := journaledServer(t, Setup())

	_, _ = do(t, http.MethodPost, m.URL("/orders?page=2"), http.Header{"Content-Type": {"application/json"}}, []byte(`{"id": 7}`))
	_, _ = do(t, http.MethodPost, m.URL("/form"), http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte("a=1&b=2"))

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("doc", "a.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(part, "hello")
	require.NoError(t, w.Close())
	_, _ = do(t, http.MethodPost, m.URL("/upload"), http.Header{"Content-Type": {w.FormDataContentType()}}, body.Bytes())

	ft := testmock.NewFakeT()

	require.True(t, m.AssertRequested(ft, 1, vlatte.JournalQuery{}, Request().Method("POST").Query("page", match.Equal("2"))))
	require.True(t, m.AssertRequested(ft, 1, vlatte.JournalQuery{}, Request().Body(match.JSONPath("$.id", match.Equal(7)))))
	require.True(t, m.AssertRequested(ft, 1, vlatte.JournalQuery{}, Request().FormField("b", match.Equal("2"))))
	require.True(t, m.AssertRequested(ft, 1, vlatte.JournalQuery{}, Request().FormFile("doc", FileWith().Filename(match.Equal("a.txt")).Body(match.Equal("hello")))))
	require.True(t, m.AssertRequested(ft, 1, vlatte.JournalQuery{}, Request().RequestMatches(func(r *http.Request) (bool, error) {
		return r.URL.Path == "/form", nil
	})))
	require.True(t, m.AssertNotRequested(ft, vlatte.JournalQuery{}, Request().Query("page", match.Equal("3"))))
	require.True(t, m.AssertRequested(ft, 3, vlatte.JournalQuery{Method: match.Equal("POST")}, Request().URLPath(match.HasPrefix("/"))))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestJournal_RefusesPatternsThatCountRequests(t *testing.T) {
	// Times and scenarios match by what the server served since, not by the request: as patterns they'd verify
	// nothing about it.
	m := journaledServer(t, Setup())

	for name, pattern := range map[string]*Builder{
		"once":     Request().Once(),
		"scenario": Request().ScenarioIs("s"),
		"broken":   Request().Times(0),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := m.CountRequests(t.Context(), vlatte.JournalQuery{}, pattern)
			require.Error(t, err)
		})
	}

	_, err := m.CountRequests(t.Context(), vlatte.JournalQuery{}, vlatte.Builder[*Mock, *Server](FromBytes([]byte("{}"), ".json")))
	require.ErrorContains(t, err, "is a *Builder")
}

func TestJournal_KeepsRequestsAcrossRestarts(t *testing.T) {
	// A server with a journal directory finds the requests it served before it stopped.
	dir := t.TempDir()

	first := MustNew(Setup().Silent().Journal(dir))
	first.MustStart()
	_, _ = do(t, http.MethodGet, first.URL("/before"), nil, nil)
	first.Close()

	second := MustNew(Setup().Silent().Journal(dir))
	second.MustStart()
	t.Cleanup(second.Close)

	ft := testmock.NewFakeT()
	require.True(t, second.AssertRequested(ft, 1, vlatte.JournalQuery{Path: match.Equal("/before")}))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestJournal_ATemporaryJournalGoesWithTheServer(t *testing.T) {
	m := MustNew(Setup().Silent().Journal(""))
	m.MustStart()

	dir := m.Journal().Dir()
	require.DirExists(t, dir)

	m.Close()
	require.NoDirExists(t, dir)
}

func TestJournal_AServerWithoutOneCantVerify(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	_, err := m.CountRequests(t.Context(), vlatte.JournalQuery{})
	require.ErrorIs(t, err, vlatte.ErrNoJournal)
}

func TestJournal_ATunnelIsOneEntry(t *testing.T) {
	// A CONNECT through the forward proxy is a request too: one entry, when its tunnel opens.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(target.Close)

	m := journaledServer(t, Setup().Proxy())

	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := target.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)

	client := &http.Client{Transport: transport}
	t.Cleanup(client.CloseIdleConnections)

	res, err := client.Get(target.URL)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	e := only(t, m, vlatte.JournalQuery{Method: match.Equal(http.MethodConnect)})
	require.Equal(t, vlatte.OutcomeProxied, e.Outcome)
	require.Equal(t, http.StatusOK, e.Status, "the status line vlatte wrote on the connection it took over")
	require.Equal(t, strings.TrimPrefix(target.URL, "https://"), e.Target)
}

func TestJournal_ASwitchKeepsItsStatus(t *testing.T) {
	// A request that switches protocols gets its 101 on the connection vlatte took over: the entry has it.
	target := switchingTarget(t, "echo")

	m := journaledServer(t, Setup().Proxy())
	m.MustMock(Getf("/ws").ID("ws").Reply(From(target.URL)))

	_, _, res := switchThrough(t, m, "/ws")
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

	q := vlatte.JournalQuery{Path: match.Equal("/ws")}
	require.Eventually(t, func() bool {
		n, err := m.CountRequests(t.Context(), q)
		return err == nil && n > 0
	}, 5*time.Second, 10*time.Millisecond)

	e := only(t, m, q)
	require.Equal(t, vlatte.OutcomeMatched, e.Outcome)
	require.Equal(t, http.StatusSwitchingProtocols, e.Status)
}

func TestJournal_AFailingJournalNeverFailsARequest(t *testing.T) {
	// The journal serves tests: a request it can't keep is still answered.
	m := journaledServer(t, Setup())
	m.MustMock(Getf("/a").Reply(OK()))

	require.NoError(t, m.Journal().Close())

	status, _ := get(t, m, "/a")
	require.Equal(t, http.StatusOK, status)

	_, err := m.CountRequests(t.Context(), vlatte.JournalQuery{})
	require.True(t, errors.Is(err, vlatte.ErrJournalClosed))
}

func TestJournal_SaysWhenItKeepsLessThanTheBody(t *testing.T) {
	// A pattern of the body of a request whose body the journal didn't keep whole would find nothing: the entry says
	// so, whether the journal's limit cut it, between characters, or the server read none of it.
	m := journaledServer(t, Setup().ConfigSources(ConfigArgs([]string{"--journal-max-body-size", "2"})))

	_, _ = do(t, http.MethodPost, m.URL("/text"), nil, []byte("aé"))
	_, _ = do(t, http.MethodGet, m.URL("/get"), nil, []byte("a body"))
	_, _ = do(t, http.MethodPost, m.URL("/whole"), nil, []byte("ab"))

	text := only(t, m, vlatte.JournalQuery{Path: match.Equal("/text")})
	require.Equal(t, "a", string(text.Body), "a character the limit cuts is left out")
	require.True(t, text.BodyTruncated)

	get := only(t, m, vlatte.JournalQuery{Path: match.Equal("/get")})
	require.Empty(t, get.Body)
	require.True(t, get.BodyTruncated, "the server reads no body of a GET")

	whole := only(t, m, vlatte.JournalQuery{Path: match.Equal("/whole")})
	require.Equal(t, "ab", string(whole.Body))
	require.False(t, whole.BodyTruncated)
}

func TestJournal_TheAdminAPIQueriesWithTheRequestPartOfMockFiles(t *testing.T) {
	// A client of any language verifies through the admin API with what its mock files write: the request part of
	// a mock, as it matches. The answer's VLatte-Request-Id names the request to get back.
	m := MustNew(Setup().Silent().Admin("").Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	res, _ := do(t, http.MethodPost, m.URL("/orders?page=2"), http.Header{"Content-Type": {"application/json"}}, []byte(`{"id": 7}`))
	id := res.Header.Get(vlatte.RequestIDHeader)
	require.NotEmpty(t, id)

	_, _ = do(t, http.MethodPost, m.URL("/orders?page=3"), nil, nil)

	query := func(body string) (*http.Response, map[string]any) {
		t.Helper()

		req, err := http.NewRequestWithContext(t.Context(), "QUERY", m.AdminURL()+"/requests", strings.NewReader(body))
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer res.Body.Close()

		var doc map[string]any
		require.NoError(t, json.UnmarshalRead(res.Body, &doc))

		return res, doc
	}

	res, doc := query(`{"limit": 0, "request": {"method": "POST", "query": {"page": "2"}, "body": ["jsonpath", "$.id", ["equal", 7]]}}`)
	require.Equal(t, http.StatusOK, res.StatusCode, doc)
	require.Equal(t, float64(1), doc["count"])

	res, doc = query(`{"request": {"url": "/orders", "path": "/orders"}}`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode, doc)
	require.Equal(t, "invalid_body", doc["code"])
	require.Contains(t, fmt.Sprint(doc["problems"]), "request.path")

	got, err := http.Get(m.AdminURL() + "/requests/" + id)
	require.NoError(t, err)

	defer got.Body.Close()

	var entry map[string]any
	require.NoError(t, json.UnmarshalRead(got.Body, &entry))
	require.Equal(t, http.StatusOK, got.StatusCode, entry)
	require.Equal(t, m.URL("/orders?page=2"), entry["target"])
	require.Equal(t, `{"id": 7}`, entry["body"])
}
