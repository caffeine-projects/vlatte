package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

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
