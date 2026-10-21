package vlatte

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJournal_IndexForgetsTheEntriesItDrops(t *testing.T) {
	// A server that runs for long must not grow its memory with every request it ever served: the index holds the
	// entries the journal keeps, and the lists keep at most a segment of dropped ones until the next one starts.
	j, err := NewJournal(JournalOptions{Dir: t.TempDir(), MaxEntries: 8})
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	for i := range 100 {
		require.NoError(t, j.Append(&JournalEntry{ID: strconv.Itoa(i), Kind: "http", Path: "/" + strconv.Itoa(i)}))
	}

	require.Len(t, j.rows, 8)
	require.Len(t, j.index.ids, 8)
	require.LessOrEqual(t, len(j.index.kind.lists["http"]), 8+j.perSegment)
	require.LessOrEqual(t, len(j.index.path.lists), 8+j.perSegment)
	require.Len(t, j.index.path.keys, len(j.index.path.lists), "values go with their lists")
}
