package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

// plainMock is a mock that can't describe itself, as gRPC mocks can't.
type plainMock struct {
	id string
}

func (m *plainMock) ID() string            { return m.id }
func (m *plainMock) Name() string          { return "" }
func (m *plainMock) Priority() int         { return 0 }
func (m *plainMock) Hits() int64           { return 0 }
func (m *plainMock) Called() bool          { return false }
func (m *plainMock) Enabled() bool         { return true }
func (m *plainMock) Enable()               {}
func (m *plainMock) Disable()              {}
func (m *plainMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *plainMock) String() string        { return m.id }

func TestProblems_Routing(t *testing.T) {
	// Requests the admin API has no route for are problem documents too, so clients read every error one way.
	url := serve(t, newStore(t))

	t.Run("a path it doesn't have", func(t *testing.T) {
		res, body := send(t, request{method: http.MethodGet, url: url + "/nope"})

		doc := problem(t, res, body, http.StatusNotFound, "route_not_found")
		require.Equal(t, "no route has the path /nope", doc["detail"])
	})

	t.Run("a method its path doesn't take", func(t *testing.T) {
		res, body := send(t, request{method: http.MethodPost, url: url + "/health"})

		doc := problem(t, res, body, http.StatusMethodNotAllowed, "method_not_allowed")
		require.Equal(t, "GET, HEAD", res.Header.Get("Allow"))
		require.Equal(t, "/health doesn't take POST: it takes GET, HEAD", doc["detail"])
	})

	t.Run("a path ServeMux cleans", func(t *testing.T) {
		// ServeMux redirects to the clean path, whether it has a route or not: the redirect is no problem, and the clean
		// path answers.
		res, body := send(t, request{method: http.MethodGet, url: url + "//mocks"})

		require.Equal(t, http.StatusOK, res.StatusCode, body)
		require.Equal(t, "/mocks", res.Request.URL.Path)
		conforms(t, res, body, "MockList")

		res, body = send(t, request{method: http.MethodGet, url: url + "//nope"})

		problem(t, res, body, http.StatusNotFound, "route_not_found")
		require.NotNil(t, res.Request.Response, "redirected")
	})

	t.Run("in yaml", func(t *testing.T) {
		for _, accept := range []string{"application/yaml", "application/problem+yaml", "application/json;q=0.5, application/problem+yaml"} {
			res, body := send(t, request{method: http.MethodGet, url: url + "/nope", accept: accept})

			require.Equal(t, "application/problem+yaml", res.Header.Get("Content-Type"), accept)
			problem(t, res, body, http.StatusNotFound, "route_not_found")
		}
	})
}

func TestProblems_PathsThatAreNotUTF8(t *testing.T) {
	// A request's path can hold any bytes, which JSON and YAML can't: the problem document says the path with the bytes
	// it can't hold replaced, and is a problem document still.
	url := serve(t, newStore(t))

	for _, accept := range []string{"application/json", "application/yaml"} {
		res, body := send(t, request{method: http.MethodGet, url: url + "/%FF", accept: accept})

		require.Equal(t, http.StatusNotFound, res.StatusCode, body)
		require.Contains(t, res.Header.Get("Content-Type"), "application/problem+", body)

		doc := conforms(t, res, body, "Problem")
		require.Equal(t, "route_not_found", doc["code"])
		require.Equal(t, "/\uFFFD", doc["instance"])
	}
}

func TestProblems_NotAcceptableIsJSON(t *testing.T) {
	// A client that takes neither format still gets an answer it can read: JSON.
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: url + "/mocks", accept: "application/xml"})

	require.Equal(t, "application/problem+json", res.Header.Get("Content-Type"))
	problem(t, res, body, http.StatusNotAcceptable, "not_acceptable")
}

func TestProblems_UnsupportedMediaTypeSaysWhatIs(t *testing.T) {
	// Every route that reads a body reads JSON or YAML only. RFC 9110: a 415 can list the media types the server takes.
	files := &fakeFiles{}
	url := serve(t, newStore(t, newFake("hello")), adminapi.Capabilities{Files: files, Recorder: &fakeRecorder{}, Scenarios: matchscenario.New(), Journal: journalOf(t)})

	for _, route := range []string{
		"POST /mocks", "POST /mocks/validate", "PUT /mocks/hello", "PATCH /mocks/hello", "POST /mocks/save",
		"PUT /scenarios/checkout", "POST /recording", "QUERY /requests",
	} {
		method, path, _ := strings.Cut(route, " ")
		res, body := send(t, request{method: method, url: url + path, contentType: "text/plain", body: "id: hello"})

		problem(t, res, body, http.StatusUnsupportedMediaType, "unsupported_media_type")
		require.Equal(t, "application/json, application/yaml", res.Header.Get("Accept"), route)
	}

	require.Empty(t, files.dir, "nothing was saved")
}

func TestProblems_CrossOrigin(t *testing.T) {
	// The admin API listens on a local address without authentication: a web page the user visits must not change
	// it. A browser's cross-site request that changes state is refused; one that reads isn't.
	store := newStore(t, newFake("hello"))
	url := serve(t, store)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			r, err := http.NewRequestWithContext(t.Context(), method, url+"/mocks/hello", nil)
			require.NoError(t, err)
			r.Header.Set("Sec-Fetch-Site", "cross-site")

			res, err := http.DefaultClient.Do(r)
			require.NoError(t, err)

			body := readBody(t, res)
			problem(t, res, body, http.StatusForbidden, "cross_origin")
		})
	}

	require.Equal(t, []string{"hello"}, ids(t, store), "nothing changed")

	r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/mocks/hello", nil)
	require.NoError(t, err)
	r.Header.Set("Sec-Fetch-Site", "cross-site")

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)
	readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestProblems_NotSupported(t *testing.T) {
	// A mock server whose protocol can't build mocks from documents, or describe them, as gRPC's can't, answers 501
	// to those operations: its admin API is the same, and says what it can't do.
	store := vlatte.NewStore[*plainMock]()
	require.NoError(t, store.Store(t.Context(), &plainMock{id: "hello"}))

	s, err := admin.New(admin.Options{}, adminapi.Capabilities{Mocks: adminapi.MocksOf[*plainMock](store, nil)})
	require.NoError(t, err)
	require.NoError(t, s.Start())
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	tests := []struct {
		method     string
		path       string
		wantDetail string
	}{
		{method: http.MethodPost, path: "/mocks", wantDetail: "this mock server can't build mocks from documents"},
		{method: http.MethodPost, path: "/mocks/validate", wantDetail: "this mock server can't build mocks from documents"},
		{method: http.MethodPut, path: "/mocks/hello", wantDetail: "this mock server can't build mocks from documents"},
		{method: http.MethodGet, path: "/mocks/hello", wantDetail: "this mock server can't describe its mocks"},
		{method: http.MethodPost, path: "/mocks/reload", wantDetail: "this mock server can't load mock files"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			res, body := send(t, request{method: tt.method, url: s.URL() + tt.path, body: `{"id": "hello"}`})

			doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
			require.Equal(t, tt.wantDetail, doc["detail"])
		})
	}

	res, body := send(t, request{method: http.MethodGet, url: s.URL() + "/mocks"})
	require.Equal(t, http.StatusOK, res.StatusCode, body)
}

func TestProblems_Closing(t *testing.T) {
	// A request that comes as the admin API closes starts no operation, and says why.
	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mocks", nil)

	handler.Closing().ServeHTTP(rec, r)

	res := rec.Result()
	res.Request = r
	problem(t, res, rec.Body.String(), http.StatusServiceUnavailable, "closing")
	require.Equal(t, "close", res.Header.Get("Connection"))
}

func TestProblem_ErrorsTheCallerCantFixAreInternalButABodyTooLarge(t *testing.T) {
	// Another way in than HTTP answers an operation's error as the admin API does: the document a route would answer.
	tests := []struct {
		name string
		err  error
		want handler.ProblemDocument
	}{
		{
			name: "a body over its limit",
			err:  &http.MaxBytesError{Limit: 10},
			want: handler.ProblemDocument{
				Status: 413, Title: "Request Entity Too Large", Code: "body_too_large",
				Detail: "the body is over the limit of 10 bytes", Instance: "add_mock",
			},
		},
		{
			name: "a store that failed",
			err:  errors.New("store: broken \xff"),
			want: handler.ProblemDocument{
				Status: 500, Title: "Internal Server Error", Code: "internal",
				Detail: "store: broken �", Instance: "add_mock",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, handler.Problem(tt.err, "add_mock"))
		})
	}
}
