package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateMockTool(t *testing.T) {
	// An agent checks a mock before it adds it: a valid one is summed up, and nothing is added either way.
	store := newStore(t)
	cs := connect(t, store)

	doc := conformsAs(t, call(t, cs, "validate_mock", map[string]any{"mock": map[string]any{"id": "hello"}}), "json", "MockSummary")
	require.Equal(t, "hello", doc["id"])

	callFails(t, cs, "validate_mock", map[string]any{"mock": map[string]any{"bad": 1}}, http.StatusBadRequest, "invalid_body")
	require.Empty(t, ids(t, store), "validating adds nothing")
}
