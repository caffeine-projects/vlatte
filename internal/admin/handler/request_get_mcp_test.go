package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestGetRequestTool(t *testing.T) {
	j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
	cs := connect(t, newStore(t), adminapi.Capabilities{Journal: j})

	doc := conformsAs(t, call(t, cs, "get_request", map[string]any{"id": "a"}), "json", "JournalEntry")
	require.Equal(t, "a", doc["id"])
	require.Equal(t, `{"id": 7}`, doc["body"])

	callFails(t, cs, "get_request", map[string]any{"id": "nope"}, http.StatusNotFound, "request_not_found")
}
