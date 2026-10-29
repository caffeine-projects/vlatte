package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestVerifyRequestsTool(t *testing.T) {
	// An agent checks that the code it changed sent the request it should, and learns why when it didn't.
	j := journalOf(t,
		entryOf("a", "GET", "/a", journal.OutcomeMatched),
		entryOf("b", "POST", "/a", journal.OutcomeUnmatched),
	)
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := conformsAs(t, call(t, cs, "verify_requests", map[string]any{"check": map[string]any{"path": "/a", "count": 2}}), "json", "RequestVerdict")
	require.Equal(t, map[string]any{"passed": true, "count": int64(2), "waited_ms": int64(0)}, doc)

	doc = conformsAs(t, call(t, cs, "verify_requests", map[string]any{"check": map[string]any{"method": "PUT", "path": "/a"}}), "json", "RequestVerdict")
	require.Equal(t, false, doc["passed"])
	require.Contains(t, doc["report"], "\nExpected at least 1 request(s) with method, path.\nGot 0.\n")

	callFails(t, cs, "verify_requests", map[string]any{"check": map[string]any{"within": "1m"}}, http.StatusBadRequest, "invalid_body")
}
