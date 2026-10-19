package admin

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Mock = (*fakeMock)(nil)

// _client gets answers as they are sent: a redirect is an answer, not followed.
var _client = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// fakeMock is the least the admin API needs to store a mock and describe it.
type fakeMock struct {
	Key string `json:"id"`
}

func (m *fakeMock) ID() string            { return m.Key }
func (m *fakeMock) Name() string          { return m.Key }
func (m *fakeMock) Priority() int         { return 0 }
func (m *fakeMock) Hits() int64           { return 0 }
func (m *fakeMock) Called() bool          { return false }
func (m *fakeMock) Enabled() bool         { return true }
func (m *fakeMock) Enable()               {}
func (m *fakeMock) Disable()              {}
func (m *fakeMock) Events() vlatte.Events { return vlatte.Events{} }
func (m *fakeMock) String() string        { return m.Key }

// newAdminServer serves an admin API over an empty store. Its builder takes {"id": "..."} and fails on anything else.
func newAdminServer(t *testing.T) *httptest.Server {
	t.Helper()

	builder := vlatte.MockBuilderFunc[*fakeMock](func(content []byte, _ string) (*fakeMock, error) {
		m := &fakeMock{}
		if err := json.Unmarshal(content, m, json.RejectUnknownMembers(true)); err != nil {
			return nil, err
		}

		if m.Key == "" {
			return nil, errors.New("fake mock: no id")
		}

		return m, nil
	})

	srv := httptest.NewServer(New(vlatte.NewStore[*fakeMock](), builder).Init())
	t.Cleanup(srv.Close)

	return srv
}

// send makes a request to the admin API and returns its response, with the body read.
func send(t *testing.T, method, url, contentType, body string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	res, err := _client.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, string(b)
}

func TestAdmin_Ping(t *testing.T) {
	// Clients wait on ping to know the admin API is up.
	srv := newAdminServer(t)

	res, body := send(t, http.MethodGet, srv.URL+"/ping", "", "")

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "pong", body)
}

func TestAdmin_AddThenGet(t *testing.T) {
	// A mock added through the admin API can be read back from where the Location header points.
	srv := newAdminServer(t)

	res, _ := send(t, http.MethodPost, srv.URL+"/mocks", "application/json", `{"id":"m1"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "/mocks/m1", res.Header.Get("Location"))

	res, body := send(t, http.MethodGet, srv.URL+"/mocks/m1", "", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json; charset=UTF-8", res.Header.Get("Content-Type"))
	require.JSONEq(t, `{"id":"m1"}`, body)
}

func TestAdmin_AddAcceptsATrailingSlash(t *testing.T) {
	// Clients written against the chi router may post to /mocks/: it still adds the mock.
	srv := newAdminServer(t)

	res, _ := send(t, http.MethodPost, srv.URL+"/mocks/", "", `{"id":"m1"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, _ = send(t, http.MethodGet, srv.URL+"/mocks/m1", "", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestAdmin_AddRejects(t *testing.T) {
	// A mock the admin API can't take is refused with a status that names the client's mistake.
	tests := []struct {
		name            string
		giveContentType string
		giveBody        string
		wantStatus      int
	}{
		{
			name:            "an id already in use",
			giveContentType: "application/json",
			giveBody:        `{"id":"m1"}`,
			wantStatus:      http.StatusConflict,
		},
		{
			name:            "a malformed mock",
			giveContentType: "application/json",
			giveBody:        `{"id":`,
			wantStatus:      http.StatusBadRequest,
		},
		{
			name:            "a content type of an unsupported format",
			giveContentType: "application/xml",
			giveBody:        `{"id":"m2"}`,
			wantStatus:      http.StatusUnsupportedMediaType,
		},
		{
			name:            "an unknown content type",
			giveContentType: "application/x-vlatte-unknown",
			giveBody:        `{"id":"m2"}`,
			wantStatus:      http.StatusUnsupportedMediaType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newAdminServer(t)

			res, _ := send(t, http.MethodPost, srv.URL+"/mocks", "", `{"id":"m1"}`)
			require.Equal(t, http.StatusCreated, res.StatusCode)

			res, _ = send(t, http.MethodPost, srv.URL+"/mocks", tt.giveContentType, tt.giveBody)
			require.Equal(t, tt.wantStatus, res.StatusCode)
		})
	}
}

func TestAdmin_RoutesAnswerOnlyTheirMethods(t *testing.T) {
	// A known route called with another method is a 405 that names the methods it takes, not a 404.
	tests := []struct {
		giveMethod string
		givePath   string
		wantAllow  string
	}{
		{giveMethod: http.MethodGet, givePath: "/mocks", wantAllow: http.MethodPost},
		{giveMethod: http.MethodDelete, givePath: "/ping", wantAllow: http.MethodGet},
		{giveMethod: "PURGE", givePath: "/mocks/m1", wantAllow: http.MethodGet},
	}

	srv := newAdminServer(t)

	for _, tt := range tests {
		t.Run(tt.giveMethod+" "+tt.givePath, func(t *testing.T) {
			res, _ := send(t, tt.giveMethod, srv.URL+tt.givePath, "", "")

			require.Equal(t, http.StatusMethodNotAllowed, res.StatusCode)
			require.Contains(t, res.Header.Get("Allow"), tt.wantAllow)
		})
	}
}

func TestAdmin_UnknownIsNotFound(t *testing.T) {
	// Paths the admin API doesn't have, and mocks it doesn't hold, are a 404, not a redirect or a server error.
	srv := newAdminServer(t)

	res, _ := send(t, http.MethodPost, srv.URL+"/mocks", "", `{"id":"m1"}`)
	require.Equal(t, http.StatusCreated, res.StatusCode)

	for _, path := range []string{"/", "/nope", "/mocks/missing", "/mocks/m1/more"} {
		t.Run(path, func(t *testing.T) {
			res, _ := send(t, http.MethodGet, srv.URL+path, "", "")

			require.Equal(t, http.StatusNotFound, res.StatusCode)
		})
	}
}
