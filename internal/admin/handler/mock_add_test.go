package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddMock(t *testing.T) {
	// A mock added reads back from where Location points.
	store := newStore(t)
	url := serve(t, store)

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks", contentType: "application/json", body: `{"id": "hello", "name": "Hello"}`})

	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	require.Equal(t, "/mocks/hello", res.Header.Get("Location"))
	doc := conforms(t, res, body, "MockSummary")
	require.Equal(t, map[string]any{"id": "hello", "name": "Hello", "priority": int64(0), "enabled": true, "hits": int64(0), "origin": "admin"}, doc)

	res, body = send(t, request{method: http.MethodGet, url: url + res.Header.Get("Location")})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.JSONEq(t, `{"id": "hello", "name": "Hello"}`, body)
}

func TestAddMock_WithoutAnIDGetsOne(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks", body: `{}`})

	require.Equal(t, http.StatusCreated, res.StatusCode, body)
	require.Equal(t, "/mocks/generated-1", res.Header.Get("Location"))
}

func TestAddMock_YAML(t *testing.T) {
	// A mock written in YAML can be posted as it is, and answers come in YAML when the client asks for it.
	store := newStore(t)
	url := serve(t, store)

	for _, contentType := range []string{"application/yaml", "application/x-yaml", "text/yaml; charset=utf-8", "text/x-yaml"} {
		t.Run(contentType, func(t *testing.T) {
			id := strings.NewReplacer("/", "-", ";", "", " ", "", "=", "-").Replace(contentType)

			res, body := send(t, request{
				method: http.MethodPost, url: url + "/mocks", contentType: contentType, accept: "application/yaml",
				body: "id: " + id + "\nresponse:\n  status: 201\n",
			})

			require.Equal(t, http.StatusCreated, res.StatusCode, body)
			require.Equal(t, "application/yaml", res.Header.Get("Content-Type"))
			doc := conforms(t, res, body, "MockSummary")
			require.Equal(t, id, doc["id"])
		})
	}

	require.Len(t, ids(t, store), 4)
}

func TestAddMock_Refused(t *testing.T) {
	// A mock the admin API can't take is refused with a status that names the client's mistake, and nothing changes.
	// A built mock the store refuses is closed: a proxy mock kept its HTTP client (#49).
	tests := []struct {
		name        string
		contentType string
		accept      string
		body        string
		wantStatus  int
		wantCode    string
		wantClosed  bool
	}{
		{
			name:       "an id in use",
			body:       `{"id": "taken"}`,
			wantStatus: http.StatusConflict,
			wantCode:   "duplicate_id",
			wantClosed: true,
		},
		{
			name:       "a malformed mock",
			body:       `{"id":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_body",
		},
		{
			name:       "a document that is no mock",
			body:       `[1]`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_body",
		},
		{
			name:        "a media type that isn't JSON or YAML",
			contentType: "application/xml",
			body:        `<mock/>`,
			wantStatus:  http.StatusUnsupportedMediaType,
			wantCode:    "unsupported_media_type",
		},
		{
			name:       "an answer in a format the admin API doesn't write",
			accept:     "application/xml",
			body:       `{"id": "new"}`,
			wantStatus: http.StatusNotAcceptable,
			wantCode:   "not_acceptable",
		},
		{
			name:       "a body too large",
			body:       `{"id": "big", "body": "` + strings.Repeat("x", 10<<20) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "body_too_large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taken := newFake("taken")
			store := newStore(t, taken)
			b := &fakeBuilder{}
			url := serveWith(t, store, b)

			res, body := send(t, request{method: http.MethodPost, url: url + "/mocks", contentType: tt.contentType, accept: tt.accept, body: tt.body})

			problem(t, res, body, tt.wantStatus, tt.wantCode)
			require.Equal(t, []string{"taken"}, ids(t, store), "nothing changed")
			require.Zero(t, taken.closed.Load())

			if tt.wantClosed {
				require.Len(t, b.built, 1)
				require.Equal(t, int64(1), b.built[0].closed.Load(), "the refused mock is closed")
			}
		})
	}
}

func TestAddMock_ProblemsOfTheMock(t *testing.T) {
	// The problems of a mock reach the client one by one, as the mock file loader reports them, in the format it
	// asked for: the body of a 400 was empty.
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks", accept: "application/yaml", body: `{"bad": 1}`})

	require.Equal(t, "application/problem+yaml", res.Header.Get("Content-Type"))
	doc := problem(t, res, body, http.StatusBadRequest, "invalid_body")
	require.Equal(t, "the mock has 1 problem", doc["detail"])
	require.Equal(t, []any{map[string]any{
		"code": "unknown_key", "origin": "admin request", "key": "bad", "message": `unknown key "bad"`,
	}}, doc["problems"])
}
