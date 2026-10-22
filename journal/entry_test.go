package journal_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestEntry_ReadsBackTheJSONItWrites(t *testing.T) {
	// The admin API answers entries in the JSON of the journal's files, and a Go client reads them back into entries:
	// every field comes back as it was, a body that isn't text and the elapsed time included.
	want := entry("a", func(e *journal.Entry) {
		e.Seq, e.MockID, e.Elapsed = 7, "m1", 1500*time.Microsecond
		e.Header = map[string][]string{"Accept": {"*/*"}}
		e.Body, e.BodyTruncated = []byte{0xff, 0x00}, true
		e.Message = "why"
		e.NearMisses = []vlatte.NearMiss{{MockID: "m2", Passed: 1, Total: 2, Skipped: []string{"times"}}}
	})

	var data bytes.Buffer
	require.NoError(t, want.MarshalJSONTo(jsontext.NewEncoder(&data)))

	got := &journal.Entry{}
	require.NoError(t, got.UnmarshalJSONFrom(jsontext.NewDecoder(&data)))
	require.Equal(t, want, got)

	// JSON that isn't an entry's names what of it isn't.
	err := (&journal.Entry{}).UnmarshalJSONFrom(jsontext.NewDecoder(strings.NewReader(`{"seq":"seven"}`)))
	semantic, ok := errors.AsType[*json.SemanticError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, jsontext.Pointer("/seq"), semantic.JSONPointer)
}
