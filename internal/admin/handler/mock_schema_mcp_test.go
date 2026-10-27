package handler_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestMockSchemaTool(t *testing.T) {
	// An agent writes mocks the server takes from the schema of its mock files, as the server has it.
	schema := `{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object"}`
	cs := connect(t, newStore(t), adminapi.Capabilities{MockSchema: []byte(schema)})

	require.JSONEq(t, schema, call(t, cs, "mock_schema", nil))
}
