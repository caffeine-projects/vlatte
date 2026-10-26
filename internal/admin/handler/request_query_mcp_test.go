package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestQueryRequestsTool(t *testing.T) {
	// An agent finds the requests no mock matched, to write the mocks they need.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeUnmatched),
	)
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := conformsAs(t, call(t, cs, "query_requests", map[string]any{"query": map[string]any{"outcome": "unmatched"}}), "json", "RequestList")
	require.Equal(t, []string{"b"}, requestIDs(doc))
	require.Equal(t, int64(1), doc["count"])

	callFails(t, cs, "query_requests", map[string]any{"query": map[string]any{"outcome": "lost"}}, http.StatusBadRequest, "invalid_body")
}
