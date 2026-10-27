package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
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
func only(t *testing.T, m *Server, q journal.Query) *journal.Entry {
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
		// What a reply writes on a connection it took over is no status the server sent: the entry has none.
		Getf("/answers-on-its-own").ID("answers-on-its-own").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}

			_, _ = io.WriteString(conn, "HTTP/1.1 202 Accepted\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			conn.Close()
		})),
	)

	tests := []struct {
		name    string
		server  *Server
		path    string
		outcome journal.Outcome
		status  int
		mockID  string
		message string
	}{
		{name: "matched", server: m, path: "/matched", outcome: journal.OutcomeMatched, status: http.StatusOK, mockID: "matched"},
		{name: "unmatched", server: m, path: "/nothing", outcome: journal.OutcomeUnmatched, status: StatusNoMatch},
		{name: "proxied", server: proxied, path: "/up", outcome: journal.OutcomeProxied, status: http.StatusAccepted},
		{name: "failed", server: m, path: "/failed", outcome: journal.OutcomeFailed, status: 599, mockID: "failed", message: "missing.txt"},
		{name: "panicked", server: m, path: "/panics", outcome: journal.OutcomeFailed, status: 599, mockID: "panics", message: "panicked: boom"},
		{name: "aborted", server: m, path: "/aborts", outcome: journal.OutcomeFailed, mockID: "aborts", message: "abort Handler"},
		{name: "exited", server: m, path: "/exits", outcome: journal.OutcomeFailed, mockID: "exits", message: "ended without an answer"},
		{name: "aborted once its client left", server: m, path: "/aborts-late", outcome: journal.OutcomeAbandoned, mockID: "aborts-late", message: "canceled"},
		{name: "panicked on a connection taken over", server: m, path: "/takes-over", outcome: journal.OutcomeFailed, mockID: "takes-over", message: "panicked: taken"},
		{name: "answered on a connection taken over", server: m, path: "/answers-on-its-own", outcome: journal.OutcomeMatched, mockID: "answers-on-its-own"},
		{name: "abandoned", server: m, path: "/slow", outcome: journal.OutcomeAbandoned, mockID: "slow", message: "left during the delay"},
		{name: "handler", server: m, path: "/handler", outcome: journal.OutcomeMatched, status: http.StatusCreated, mockID: "handler"},
		{name: "handler writing nothing", server: m, path: "/silent", outcome: journal.OutcomeMatched, status: http.StatusOK, mockID: "silent"},
		{name: "echo", server: m, path: "/echo", outcome: journal.OutcomeMatched, status: http.StatusOK, mockID: "echo"},
		{name: "failed after its status", server: m, path: "/late", outcome: journal.OutcomeFailed, status: http.StatusAccepted, mockID: "late", message: "late failure"},
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

			q := journal.Query{Path: match.Equal(tt.path)}

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

	m.MustMock(Postf("/a").Callback(func(_ context.Context, in vlatte.OnComplete) error {
		in.(*OnComplete).Request().RawRequest.Header.Set("X-Changed", "later")
		return nil
	}).Reply(OK()))

	res, _ := do(t, http.MethodPost, m.URL("/a?q=1"), http.Header{"Authorization": {"Bearer secret"}, "X-Mine": {"1"}}, []byte("123456"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	e := only(t, m, journal.Query{})
	require.Equal(t, "1", http.Header(e.Header).Get("X-Mine"))
	require.Empty(t, http.Header(e.Header).Get("X-Changed"))
	require.NotContains(t, http.Header(e.Header).Get("Authorization"), "secret")
	require.Equal(t, strings.TrimPrefix(m.URL(), "http://"), http.Header(e.Header).Get("Host"))
	require.Equal(t, "1234", string(e.Body))
	require.True(t, e.BodyTruncated)
	require.Equal(t, m.URL("/a?q=1"), e.Target)
	require.Equal(t, "/a", e.Path)
}

func TestJournal_AMatchedRequestTellsTheEventsThatFailed(t *testing.T) {
	// An on_complete event, such as a webhook, runs once the mock answered, and never changes the answer: the entry
	// is where a test sees that one failed, and why, without reading the logs. The request still counts as matched.
	m := journaledServer(t, Setup())

	fails := func(msg string) vlatte.EventFunc[vlatte.OnComplete] {
		return func(context.Context, vlatte.OnComplete) error { return errors.New(msg) }
	}
	succeeds := func(context.Context, vlatte.OnComplete) error { return nil }

	m.MustMock(
		Getf("/failed").Callback(fails("target answered 500")).Callback(succeeds).Callback(fails("target unreachable")).Reply(OK()),
		Getf("/ok").Callback(succeeds).Reply(OK()),
	)

	for _, path := range []string{"/failed", "/ok"} {
		res, _ := do(t, http.MethodGet, m.URL(path), nil, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
	}

	failed := only(t, m, journal.Query{Path: match.Equal("/failed")})
	require.Equal(t, journal.OutcomeMatched, failed.Outcome)
	require.Equal(t, http.StatusOK, failed.Status)
	// The events run at the same time: their errors come in any order.
	require.ElementsMatch(t, []string{
		"on_complete listener callback at index 0: target answered 500",
		"on_complete listener callback at index 2: target unreachable",
	}, strings.Split(failed.Message, "\n"))

	ok := only(t, m, journal.Query{Path: match.Equal("/ok")})
	require.Equal(t, journal.OutcomeMatched, ok.Outcome)
	require.Empty(t, ok.Message)
}

func TestJournal_RedactsTheHostToo(t *testing.T) {
	// net/http keeps the Host header apart from the others, but it is a header the client sent all the same: a
	// server that redacts it in its logs keeps it out of its journal too. A pattern still matches its URL, which is
	// the server's.
	m := journaledServer(t, Setup().RedactHeader("Host"))

	_, _ = do(t, http.MethodGet, m.URL("/private"), nil, nil)

	e := only(t, m, journal.Query{Path: match.Equal("/private")})
	require.Equal(t, []string{"<redacted>"}, e.Header["Host"])

	n, err := m.CountRequests(t.Context(), journal.Query{}, Request().URL(match.Equal(m.URL("/private"))))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestJournal_UnmatchedEntriesNameTheClosestMocks(t *testing.T) {
	m := journaledServer(t, Setup())
	m.MustMock(Getf("/users").ID("users").Header("Accept", match.Equal("application/json")).Reply(OK()))

	_, _ = do(t, http.MethodGet, m.URL("/users"), nil, nil)

	e := only(t, m, journal.Query{Outcome: journal.OutcomeUnmatched})
	require.Equal(t, []vlatte.NearMiss{{MockID: "users", Passed: 2, Total: 3, Failed: []string{"header Accept"}}}, e.NearMisses)
}

func TestJournal_VerifiesWithRequestPatterns(t *testing.T) {
	// A test verifies with the request part of a mock, with all a mock can match: query
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

	require.True(t, m.AssertRequested(t.Context(), ft, 1, journal.Query{}, Request().Method("POST").Query("page", match.Equal("2"))))
	require.True(t, m.AssertRequested(t.Context(), ft, 1, journal.Query{}, Request().Body(match.JSONPath("$.id", match.Equal(7)))))
	require.True(t, m.AssertRequested(t.Context(), ft, 1, journal.Query{}, Request().FormField("b", match.Equal("2"))))
	require.True(t, m.AssertRequested(t.Context(), ft, 1, journal.Query{}, Request().FormFile("doc", FileWith().Filename(match.Equal("a.txt")).Body(match.Equal("hello")))))
	require.True(t, m.AssertRequested(t.Context(), ft, 1, journal.Query{}, Request().RequestMatches(func(r *http.Request) (bool, error) {
		return r.URL.Path == "/form", nil
	})))
	require.True(t, m.AssertNotRequested(t.Context(), ft, journal.Query{}, Request().Query("page", match.Equal("3"))))
	require.True(t, m.AssertRequested(t.Context(), ft, 3, journal.Query{Method: match.Equal("POST")}, Request().URLPath(match.HasPrefix("/"))))
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)
}

func TestJournal_APatternMatchesItsPathParams(t *testing.T) {
	// A pattern's path params are matchers a mock gets when it builds: a pattern took the builder's other matchers
	// only, so a pattern of /users/{id} with id 7 counted /users/8 too.
	m := journaledServer(t, Setup())

	_, _ = do(t, http.MethodGet, m.URL("/users/7"), nil, nil)
	_, _ = do(t, http.MethodGet, m.URL("/users/8"), nil, nil)

	n, err := m.CountRequests(t.Context(), journal.Query{}, Request().URLPathTemplate("/users/{id}").PathParam("id", match.Equal("7")))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	_, err = m.CountRequests(t.Context(), journal.Query{}, Request().PathParam("id", match.Equal("7")))
	require.EqualError(t, err, `journal: pattern 0: http: request pattern: path param "id": the mock has no path template: set one with URLPathTemplate`)
}

func TestJournal_APatternMatchesTheURLAProxiedRequestNames(t *testing.T) {
	// A mock matches a request to the forward proxy on the URL it names, its scheme and host included: a pattern
	// matched the journal's entry on the server's URL, so the pattern of a mock that served it counted no request.
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(host.Close)

	m := journaledServer(t, Setup().Proxy())

	res, err := proxyClient(t, m).Get(host.URL + "/up?q=1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	pattern := Request().URL(match.Equal(host.URL + "/up?q=1"))
	require.Eventually(t, func() bool {
		n, err := m.CountRequests(t.Context(), journal.Query{}, pattern)
		return err == nil && n == 1
	}, 5*time.Second, 10*time.Millisecond)
}

func TestJournal_RefusesPatternsOfTheClientAddress(t *testing.T) {
	// The journal keeps no client address: a pattern of one would count no request, and AssertNotRequested would
	// pass for a request the server got.
	m := journaledServer(t, Setup())

	_, err := m.CountRequests(t.Context(), journal.Query{}, Request().ClientIP(match.Equal("127.0.0.1")))
	require.EqualError(t, err, "journal: pattern 0: http: a request pattern has no ClientIP: the journal keeps no client address")
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
			_, err := m.CountRequests(t.Context(), journal.Query{}, pattern)
			require.Error(t, err)
		})
	}

	_, err := m.CountRequests(t.Context(), journal.Query{}, vlatte.Builder[*Mock, *Server](FromBytes([]byte("{}"), ".json")))
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
	require.True(t, second.AssertRequested(t.Context(), ft, 1, journal.Query{Path: match.Equal("/before")}))
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

func TestJournal_AServerThatCantKeepItsJournalFailsToBuild(t *testing.T) {
	// A server asked for a journal it can't keep fails as it is built, with the reason, rather than serve requests
	// a test then can't verify.
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	_, err := New(Setup().Silent().Journal(filepath.Join(file, "journal")))
	require.ErrorIs(t, err, syscall.ENOTDIR)
}

func TestJournal_AServerWithoutOneCantVerify(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	_, err := m.CountRequests(t.Context(), journal.Query{})
	require.ErrorIs(t, err, journal.ErrNoJournal)
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

	e := only(t, m, journal.Query{Method: match.Equal(http.MethodConnect)})
	require.Equal(t, journal.OutcomeProxied, e.Outcome)
	require.Equal(t, http.StatusOK, e.Status, "the status line vlatte wrote on the connection it took over")
	require.Equal(t, strings.TrimPrefix(target.URL, "https://"), e.Target)
}

func TestJournal_ARequestToTheForwardProxyKeepsTheURLItNames(t *testing.T) {
	// A request to the forward proxy names its host's URL in full: the entry's target is that URL, not the proxy's,
	// so a test verifies the calls its code made to other hosts.
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(host.Close)

	m := journaledServer(t, Setup().Proxy())

	res, err := proxyClient(t, m).Get(host.URL + "/up?q=1")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	q := journal.Query{Target: match.Equal(host.URL + "/up?q=1")}
	require.Eventually(t, func() bool {
		n, err := m.CountRequests(t.Context(), q)
		return err == nil && n == 1
	}, 5*time.Second, 10*time.Millisecond)

	e := only(t, m, q)
	require.Equal(t, journal.OutcomeProxied, e.Outcome)
	require.Equal(t, http.StatusAccepted, e.Status)
}

func TestJournal_AConnectKeepsTheAnswerItGot(t *testing.T) {
	// A CONNECT gets its 200 on the connection the proxy takes over, before the proxy knows what comes of it: a tunnel
	// that fails after it still answered 200, and its entry says so, with what failed.
	for name, tt := range map[string]struct {
		host    func(t *testing.T) string
		start   func(conn net.Conn)
		message string
	}{
		"a TLS start that fails": {
			host: func(t *testing.T) string { host, _ := acceptedHost(t); return host },
			start: func(conn net.Conn) {
				// A TLS record that is no ClientHello: the handshake fails as it reads it.
				_, _ = conn.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x08, 0x02, 0x00, 0x00, 0x04, 0x03, 0x03, 0x00, 0x00})
			},
			message: "intercepting the TLS connection",
		},
		"a host that is gone": {
			host:    closedAddr,
			start:   func(conn net.Conn) { _, _ = io.WriteString(conn, "plain\n") },
			message: "opening a tunnel",
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := journaledServer(t, Setup().Proxy(&ProxyConfig{Intercept: true}))
			host := tt.host(t)

			conn, _ := openTunnel(t, hostOf(t, m), host, "")
			tt.start(conn)

			_, _ = io.ReadAll(conn)

			q := journal.Query{Method: match.Equal(http.MethodConnect)}
			require.Eventually(t, func() bool {
				n, err := m.CountRequests(t.Context(), q)
				return err == nil && n == 1
			}, 5*time.Second, 10*time.Millisecond)

			e := only(t, m, q)
			require.Equal(t, http.StatusOK, e.Status, "the 200 the client got")
			require.Equal(t, journal.OutcomeFailed, e.Outcome)
			require.Contains(t, e.Message, tt.message)
		})
	}
}

func TestJournal_ASwitchKeepsItsStatus(t *testing.T) {
	// A request that switches protocols gets its 101 on the connection vlatte took over: the entry has it, even when
	// what runs after the switch, such as a callback, panics. The panic is the callback's, a warning, as an event's is:
	// the mock matched.
	target := switchingTarget(t, "echo")

	m := journaledServer(t, Setup().Proxy())
	m.MustMock(
		Getf("/ws").ID("ws").Reply(From(target.URL)),
		Getf("/ws-panics").ID("ws-panics").Callback(func(context.Context, vlatte.OnComplete) error { panic("after the switch") }).
			Reply(From(target.URL)),
	)

	for path, outcome := range map[string]journal.Outcome{"/ws": journal.OutcomeMatched, "/ws-panics": journal.OutcomeMatched} {
		t.Run(path, func(t *testing.T) {
			conn, _, res := switchThrough(t, m, path)
			require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)

			// The request ends with the connection it switched.
			require.NoError(t, conn.Close())

			q := journal.Query{Path: match.Equal(path)}
			require.Eventually(t, func() bool {
				n, err := m.CountRequests(t.Context(), q)
				return err == nil && n > 0
			}, 5*time.Second, 10*time.Millisecond)

			e := only(t, m, q)
			require.Equal(t, outcome, e.Outcome)
			require.Equal(t, http.StatusSwitchingProtocols, e.Status)
		})
	}
}

func TestJournal_AFailingJournalNeverFailsARequest(t *testing.T) {
	// The journal serves tests: a request it can't keep is still answered.
	m := journaledServer(t, Setup())
	m.MustMock(Getf("/a").Reply(OK()))

	require.NoError(t, m.Journal().Close())

	status, _ := get(t, m, "/a")
	require.Equal(t, http.StatusOK, status)

	_, err := m.CountRequests(t.Context(), journal.Query{})
	require.True(t, errors.Is(err, journal.ErrClosed))
}

func TestJournal_SaysWhenItKeepsLessThanTheBody(t *testing.T) {
	// A pattern of the body of a request whose body the journal didn't keep whole would find nothing: the entry says
	// so, whether the journal's limit cut it, between characters, or the server read none of it.
	m := journaledServer(t, Setup().ConfigSources(ConfigArgs([]string{"--journal-max-body-size", "2"})))

	_, _ = do(t, http.MethodPost, m.URL("/text"), nil, []byte("aé"))
	_, _ = do(t, http.MethodGet, m.URL("/get"), nil, []byte("a body"))
	_, _ = do(t, http.MethodPost, m.URL("/whole"), nil, []byte("ab"))

	text := only(t, m, journal.Query{Path: match.Equal("/text")})
	require.Equal(t, "a", string(text.Body), "a character the limit cuts is left out")
	require.True(t, text.BodyTruncated)

	get := only(t, m, journal.Query{Path: match.Equal("/get")})
	require.Empty(t, get.Body)
	require.True(t, get.BodyTruncated, "the server reads no body of a GET")

	whole := only(t, m, journal.Query{Path: match.Equal("/whole")})
	require.Equal(t, "ab", string(whole.Body))
	require.False(t, whole.BodyTruncated)
}

func TestJournal_ABodyAtTheParseLimitIsWholeWhenItsLengthSaysSo(t *testing.T) {
	// A body that fills the server's parse limit was read whole when its Content-Length is that size: a pattern of
	// it counts it. One of no length may have had more, which the entry says.
	m := journaledServer(t, Setup().MaxBodyParsingLimit(4))

	sentRequest{method: http.MethodPost, target: "/sized", body: []byte("abcd")}.send(t, m.URL(""))
	sentRequest{method: http.MethodPost, target: "/streamed", body: []byte("abcd"), chunked: true}.send(t, m.URL(""))

	require.False(t, only(t, m, journal.Query{Path: match.Equal("/sized")}).BodyTruncated)
	require.True(t, only(t, m, journal.Query{Path: match.Equal("/streamed")}).BodyTruncated)

	n, err := m.CountRequests(t.Context(), journal.Query{Path: match.Equal("/sized")}, Request().Body(match.Equal("abcd")))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestJournal_APatternCantReadABodyTheJournalCut(t *testing.T) {
	// A pattern that reads the body of a request whose body the journal didn't keep whole can't tell whether the
	// request matched: verifying fails, rather than count no request, so AssertNotRequested can't pass for a request
	// the server got. A pattern of the rest of the request still counts it, and one the rest misses misses it.
	cut := journaledServer(t, Setup().ConfigSources(ConfigArgs([]string{"--journal-max-body-size", "64"})))
	unread := journaledServer(t, Setup().NoBodyParsing())

	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreateFormFile("upload", "a.txt")
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte("a"), 200))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	_, _ = do(t, http.MethodPost, cut.URL("/upload"), http.Header{"Content-Type": {w.FormDataContentType()}}, form.Bytes())
	_, _ = do(t, http.MethodPost, unread.URL("/text"), nil, []byte("a body"))

	tests := []struct {
		name    string
		server  *Server
		pattern *Builder
	}{
		{name: "a form file of a body cut", server: cut, pattern: Request().FormFile("upload", FileWith())},
		{name: "a body the server didn't read", server: unread, pattern: Request().Body(match.Contain("body"))},
		{name: "form fields of a body the server didn't read", server: unread, pattern: Request().FormFields("a", match.Equal([]string{"1"}))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.server.CountRequests(t.Context(), journal.Query{}, tt.pattern)
			require.ErrorIs(t, err, vlatte.ErrMatching)
			require.ErrorContains(t, err, "the journal kept part of its body")

			ft := testmock.NewFakeT()
			require.False(t, tt.server.AssertNotRequested(t.Context(), ft, journal.Query{}, tt.pattern), "it can't tell")
		})
	}

	n, err := cut.CountRequests(t.Context(), journal.Query{}, Request().URLPath(match.Equal("/upload")))
	require.NoError(t, err)
	require.Equal(t, 1, n, "a pattern of the rest of the request still reads it")

	n, err = cut.CountRequests(t.Context(), journal.Query{}, Request().URLPath(match.Equal("/other")).FormFile("upload", FileWith()))
	require.NoError(t, err)
	require.Zero(t, n, "the rest of the request differs: no body tells otherwise")
}

func TestJournal_APatternCountsOnlyTheRequestsOfItsProtocol(t *testing.T) {
	// A journal may hold the calls of another protocol, such as gRPC's, whose fields read otherwise: an HTTP pattern
	// counts none of them, even one with the path it matches.
	m := journaledServer(t, Setup())

	_, _ = do(t, http.MethodGet, m.URL("/pkg.Service/Call"), nil, nil)
	require.NoError(t, m.Journal().Append(&journal.Entry{Kind: "grpc", Path: "/pkg.Service/Call", Target: "/pkg.Service/Call"}))

	total, err := m.CountRequests(t.Context(), journal.Query{})
	require.NoError(t, err)
	require.Equal(t, 2, total, "the journal has both")

	n, err := m.CountRequests(t.Context(), journal.Query{}, Request().URLPath(match.Equal("/pkg.Service/Call")))
	require.NoError(t, err)
	require.Equal(t, 1, n, "the HTTP request only")
}

func TestJournal_APatternFailsOnAnEntryThatIsNoRequest(t *testing.T) {
	// An entry whose target is no URL, such as one appended by hand, is no request a pattern can match: verifying
	// fails, and names the entry, rather than count it out.
	m := journaledServer(t, Setup())

	broken := &journal.Entry{Kind: Kind, Method: http.MethodGet, Target: "http://[::1"}
	require.NoError(t, m.Journal().Append(broken))

	_, err := m.CountRequests(t.Context(), journal.Query{}, Request().URLPath(match.Equal("/")))

	urlErr, ok := errors.AsType[*url.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "http://[::1", urlErr.URL)
	require.ErrorContains(t, err, fmt.Sprintf("entry %d: target", broken.Seq))
}

func TestJournal_AMatcherThatFailsFailsTheVerification(t *testing.T) {
	// A matcher that can't tell whether a request matches, such as a predicate that fails, fails verifying, as it
	// fails matching, rather than count the request out: AssertNotRequested can't pass for a request the server got.
	m := journaledServer(t, Setup())
	_, _ = do(t, http.MethodGet, m.URL("/a"), nil, nil)

	errPredicate := errors.New("predicate: failed")
	pattern := Request().RequestMatches(func(*http.Request) (bool, error) { return false, errPredicate })

	_, err := m.CountRequests(t.Context(), journal.Query{}, pattern)
	require.ErrorIs(t, err, vlatte.ErrMatching)
	require.ErrorIs(t, err, errPredicate)

	ft := testmock.NewFakeT()
	require.False(t, m.AssertNotRequested(t.Context(), ft, journal.Query{}, pattern))
}

func TestJournal_APatternReadsAnEntryWithoutHeaders(t *testing.T) {
	// An entry may have no headers, such as one appended by hand: a pattern reads its request as net/http hands one
	// to a handler, with a Header, and the host of its target.
	m := journaledServer(t, Setup())

	require.NoError(t, m.Journal().Append(&journal.Entry{Kind: Kind, Method: http.MethodGet, Path: "/plain", Target: "http://example.com/plain"}))

	n, err := m.CountRequests(t.Context(), journal.Query{}, Request().URLPath(match.Equal("/plain")).RequestMatches(func(r *http.Request) (bool, error) {
		return r.Header != nil && r.Host == "example.com", nil
	}))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestJournal_TheAdminAPIQueriesWithTheRequestPartOfMockFiles(t *testing.T) {
	// A client of any language verifies through the admin API with what its mock files write: the request part of
	// a mock, as it matches. The answer's VLatte-Request-Id names the request to get back.
	m := MustNew(Setup().Silent().Admin(admin.MustNew()).Journal(""))
	m.MustStart()
	t.Cleanup(m.Close)

	res, _ := do(t, http.MethodPost, m.URL("/orders?page=2"), http.Header{"Content-Type": {"application/json"}}, []byte(`{"id": 7}`))
	id := res.Header.Get(vlatte.HeaderRequestID)
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

// rewriting is the writer of a middleware that answers with its status in place of the one the handler under it
// writes.
type rewriting struct {
	http.ResponseWriter

	status int
	wrote  bool
}

func (w *rewriting) WriteHeader(int) {
	if !w.wrote {
		w.wrote = true
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *rewriting) Write(p []byte) (int, error) {
	w.WriteHeader(0)
	return w.ResponseWriter.Write(p)
}

// unwrapping is a rewriting writer that hands out the writer under it, as http.ResponseController expects.
type unwrapping struct {
	*rewriting
}

func (w unwrapping) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// middleware is a middleware whose handler is serve.
func middleware(serve func(w http.ResponseWriter, r *http.Request, next http.Handler)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve(w, r, next) })
	}
}

func TestJournal_KeepsTheStatusTheClientGot(t *testing.T) {
	// A middleware or a decorator may answer first, or answer in place of the mock: the entry has the status the
	// client got, not the one the mock wrote, which a test of the client's code would otherwise verify in vain.
	// drops is a middleware that answers nothing of what the handler under it writes: the client gets net/http's 200.
	drops := Setup().Use(middleware(func(_ http.ResponseWriter, r *http.Request, next http.Handler) {
		next.ServeHTTP(httptest.NewRecorder(), r)
	}))

	tests := []struct {
		name    string
		setup   *ConfigBuilder
		reply   ReplierFactory
		path    string
		status  int
		outcome journal.Outcome
		mockID  string
	}{
		{
			name: "a middleware that answers first",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				w.WriteHeader(http.StatusAccepted)
				next.ServeHTTP(w, r)
			})),
			reply:   Handler(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "body") }),
			path:    "/answered",
			status:  http.StatusAccepted,
			outcome: journal.OutcomeMatched,
			mockID:  "answered",
		},
		{
			name: "a middleware that answers in place of the mock",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				next.ServeHTTP(httptest.NewRecorder(), r)
				w.WriteHeader(http.StatusServiceUnavailable)
			})),
			reply:   OK(),
			path:    "/answered",
			status:  http.StatusServiceUnavailable,
			outcome: journal.OutcomeMatched,
			mockID:  "answered",
		},
		{
			name: "a decorator that sends early hints, then its own answer",
			setup: Setup().HandlerDecorator(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				next.ServeHTTP(httptest.NewRecorder(), r)
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusConflict)
			})),
			reply:   OK(),
			path:    "/answered",
			status:  http.StatusConflict,
			outcome: journal.OutcomeMatched,
			mockID:  "answered",
		},
		{
			name: "a middleware that drops the request's context",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				next.ServeHTTP(unwrapping{&rewriting{ResponseWriter: w, status: http.StatusCreated}}, r.WithContext(context.Background()))
			})),
			reply:   OK(),
			path:    "/answered",
			status:  http.StatusCreated,
			outcome: journal.OutcomeMatched,
			mockID:  "answered",
		},
		{
			name: "a middleware whose writer doesn't unwrap",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				next.ServeHTTP(&rewriting{ResponseWriter: w, status: http.StatusCreated}, r)
			})),
			reply:   OK(),
			path:    "/answered",
			status:  http.StatusCreated,
			outcome: journal.OutcomeMatched,
			mockID:  "answered",
		},
		{
			name: "a middleware that recovers the mock's panic",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				defer func() {
					if recover() != nil {
						w.WriteHeader(http.StatusServiceUnavailable)
					}
				}()

				next.ServeHTTP(w, r)
			})),
			reply:   Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { panic("boom") }),
			path:    "/answered",
			status:  http.StatusServiceUnavailable,
			outcome: journal.OutcomeFailed,
			mockID:  "answered",
		},
		{
			name: "a middleware that swallows the mock's panic",
			setup: Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				defer func() { _ = recover() }()

				next.ServeHTTP(w, r)
			})),
			reply:   Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { panic("boom") }),
			path:    "/answered",
			status:  http.StatusOK,
			outcome: journal.OutcomeFailed,
			mockID:  "answered",
		},
		{
			name:    "a middleware that drops the answer of a mock that failed",
			setup:   drops,
			reply:   OK().BodyFile("missing.txt"),
			path:    "/answered",
			status:  http.StatusOK,
			outcome: journal.OutcomeFailed,
			mockID:  "answered",
		},
		{
			name:    "a middleware that drops the answer to a request no mock matched",
			setup:   drops,
			reply:   OK(),
			path:    "/missed",
			status:  http.StatusOK,
			outcome: journal.OutcomeUnmatched,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := journaledServer(t, tt.setup)
			m.MustMock(Getf("/answered").ID("answered").Reply(tt.reply))

			res, _ := do(t, http.MethodGet, m.URL(tt.path), nil, nil)
			require.Equal(t, tt.status, res.StatusCode)

			e := only(t, m, journal.Query{})
			require.Equal(t, tt.status, e.Status, "the status the client got")
			require.Equal(t, tt.outcome, e.Outcome)
			require.Equal(t, tt.mockID, e.MockID)
		})
	}
}

func TestJournal_AnAnswerATimeoutGaveFirstKeepsItsStatus(t *testing.T) {
	// A decorator that times the mock out, as http.TimeoutHandler does, answers the client while the mock goes on:
	// the entry the mock ends later has the status the client got.
	release := make(chan struct{})

	m := journaledServer(t, Setup().HandlerDecorator(func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, 10*time.Millisecond, "timed out")
	}))
	m.MustMock(Getf("/slow").ID("slow").Reply(Handler(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, "late")
	})))

	res, _ := do(t, http.MethodGet, m.URL("/slow"), nil, nil)
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	close(release)

	require.Eventually(t, func() bool {
		n, err := m.CountRequests(t.Context(), journal.Query{})
		return err == nil && n == 1
	}, 5*time.Second, 10*time.Millisecond)

	e := only(t, m, journal.Query{})
	require.Equal(t, http.StatusServiceUnavailable, e.Status, "the status the client got")
}

func TestJournal_AContextAMiddlewareEndsAfterwardIsNoClientThatLeft(t *testing.T) {
	// A middleware may end the context it hands the mock once the mock is done, as a timeout middleware does: the
	// entry has the outcome serving reached, not that of a client that left.
	m := journaledServer(t, Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})))
	m.MustMock(Getf("/aborts").Reply(Handler(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/aborts"), nil)
	require.NoError(t, err)

	_, err = _rawClient.Do(req)
	require.ErrorIs(t, err, io.EOF)

	e := only(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeFailed, e.Outcome)
	require.Contains(t, e.Message, http.ErrAbortHandler.Error())
}

func TestJournal_AnAnswerADecoratorAbortsHasNoStatus(t *testing.T) {
	// A decorator that aborts the answer, as a proxy does, drops the connection: the client got no status, whatever
	// the mock wrote under it.
	m := journaledServer(t, Setup().HandlerDecorator(middleware(func(_ http.ResponseWriter, r *http.Request, next http.Handler) {
		next.ServeHTTP(httptest.NewRecorder(), r)
		panic(http.ErrAbortHandler)
	})))
	m.MustMock(Getf("/aborted").ID("aborted").Reply(OK()))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/aborted"), nil)
	require.NoError(t, err)

	_, err = _rawClient.Do(req)
	require.ErrorIs(t, err, io.EOF)

	e := only(t, m, journal.Query{})
	require.Equal(t, journal.OutcomeMatched, e.Outcome)
	require.Zero(t, e.Status, "no status reached the client")
}

func TestJournal_KeepsTheStatusBehindAMiddlewareThatHidesTheAnswer(t *testing.T) {
	// A middleware that hands the mock a context of its own and a writer that doesn't unwrap hides the writer outside
	// it: the entry then has the status the mock sent, as before there was one, which is the client's but for a
	// middleware that changes it.
	body := bytes.Repeat([]byte("0123456789abcdef"), 8<<10)
	file := filepath.Join(t.TempDir(), "body.bin")
	require.NoError(t, os.WriteFile(file, body, 0o600))

	tests := []struct {
		name    string
		reply   ReplierFactory
		status  int
		outcome journal.Outcome
	}{
		{name: "a body the mock copies", reply: Created().BodyFile(file), status: http.StatusCreated, outcome: journal.OutcomeMatched},
		{
			name:    "a mock that panics",
			reply:   Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { panic("boom") }),
			status:  StatusNoMatch,
			outcome: journal.OutcomeFailed,
		},
		{
			name: "a mock that answers, then panics",
			reply: Func(func(w http.ResponseWriter, _ *RequestValues) (*Response, error) {
				w.WriteHeader(http.StatusAccepted)
				panic("boom")
			}),
			status:  http.StatusAccepted,
			outcome: journal.OutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := journaledServer(t, Setup().Use(middleware(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				next.ServeHTTP(opaqueWriter{w}, r.WithContext(context.Background()))
			})))
			m.MustMock(Getf("/hidden").Reply(tt.reply))

			res, _ := do(t, http.MethodGet, m.URL("/hidden"), nil, nil)
			require.Equal(t, tt.status, res.StatusCode)

			e := only(t, m, journal.Query{})
			require.Equal(t, tt.status, e.Status)
			require.Equal(t, tt.outcome, e.Outcome)
		})
	}
}

func TestJournal_AnInterceptedRequestKeepsTheStatusTheClientGot(t *testing.T) {
	// The requests within a tunnel the proxy intercepts go through the server's middlewares too, and their entries
	// have the status the client got, as any request's.
	target := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(target.Close)

	m := journaledServer(t, Setup().Proxy(&ProxyConfig{Intercept: true}).Use(middleware(
		func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			next.ServeHTTP(unwrapping{&rewriting{ResponseWriter: w, status: http.StatusCreated}}, r)
		})))
	m.MustMock(Getf("/mocked").Reply(OK()))

	res, err := interceptClient(t, m).Get(target.URL + "/mocked")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	e := only(t, m, journal.Query{Path: match.Equal("/mocked")})
	require.Equal(t, http.StatusCreated, e.Status)
}
