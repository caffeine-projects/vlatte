package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestMockSchema(t *testing.T) {
	// A client, such as an editor or an agent, writes mocks the server takes from the schema of its mock files: the
	// server's own document, as it gave it, or the same in YAML.
	schema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["request"]}`
	url := serve(t, newStore(t), adminapi.Capabilities{MockSchema: []byte(schema)})

	res, body := send(t, request{method: http.MethodGet, url: url + "/schema"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))
	require.Equal(t, schema, body)

	res, body = send(t, request{method: http.MethodGet, url: url + "/schema", accept: "application/yaml"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/yaml", res.Header.Get("Content-Type"))

	fromYAML, err := config.Parse([]byte(body), "yaml")
	require.NoError(t, err)

	fromJSON, err := config.Parse([]byte(schema), "json")
	require.NoError(t, err)
	require.Equal(t, fromJSON, fromYAML)
}

func TestMockSchema_NotSupported(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodGet, url: url + "/schema"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't describe its mock files", doc["detail"])
}

func TestMockSchema_OnOperationsWithoutOne(t *testing.T) {
	// The handler answers for itself, as the route does, when the server has no schema.
	ops := handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil)})

	rec := httptest.NewRecorder()
	handler.MockSchema(ops).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/schema", nil))

	require.Equal(t, http.StatusNotImplemented, rec.Code)
	require.Contains(t, rec.Body.String(), `"code": "not_supported"`)
}

func TestMockSchema_ASchemaThatIsntJSONIsTheServersProblem(t *testing.T) {
	// YAML is the server's schema converted: one that doesn't parse is an internal error, not a broken document.
	url := serve(t, newStore(t), adminapi.Capabilities{MockSchema: []byte(`{"type":`)})

	res, body := send(t, request{method: http.MethodGet, url: url + "/schema", accept: "application/yaml"})

	problem(t, res, body, http.StatusInternalServerError, "internal")
}
