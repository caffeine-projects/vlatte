package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestListRequestsTool(t *testing.T) {
	// The newest requests come first, a page at a time, with how many there are.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "GET", "/b", journal.OutcomeUnmatched),
		entryOf("c", "POST", "/c", journal.OutcomeMatched),
	)
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := conformsAs(t, call(t, cs, "list_requests", map[string]any{"limit": 2}), "json", "RequestList")
	require.Equal(t, []string{"c", "b"}, requestIDs(doc))
	require.Equal(t, int64(3), doc["count"])

	doc = conformsAs(t, call(t, cs, "list_requests", map[string]any{"limit": 2, "before": doc["next"]}), "json", "RequestList")
	require.Equal(t, []string{"a"}, requestIDs(doc))

	doc = conformsAs(t, call(t, cs, "list_requests", map[string]any{"limit": 0}), "json", "RequestList")
	require.Empty(t, requestIDs(doc), "a limit of 0 counts them only")
	require.Equal(t, int64(3), doc["count"])

	callFails(t, cs, "list_requests", map[string]any{"limit": 1001}, http.StatusBadRequest, "invalid_parameter")
}
