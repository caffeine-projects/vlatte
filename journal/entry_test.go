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
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestOutcome_KeepsItsNameInJSON(t *testing.T) {
	// Journal files, sidecar indexes and admin responses share these names.
	tests := []struct {
		name string
		want journal.Outcome
	}{
		{name: "matched", want: journal.OutcomeMatched},
		{name: "unmatched", want: journal.OutcomeUnmatched},
		{name: "proxied", want: journal.OutcomeProxied},
		{name: "failed", want: journal.OutcomeFailed},
		{name: "abandoned", want: journal.OutcomeAbandoned},
		{name: "unknown", want: journal.OutcomeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("write", func(t *testing.T) {
				data, err := json.Marshal(tt.want)
				require.NoError(t, err)
				require.Equal(t, `"`+tt.name+`"`, string(data))
			})

			t.Run("read", func(t *testing.T) {
				var got journal.Outcome
				require.NoError(t, json.Unmarshal([]byte(`"`+tt.name+`"`), &got))
				require.Equal(t, tt.want, got)
			})
		})
	}
}

func TestOutcome_ParsesNamesInAnyCase(t *testing.T) {
	tests := []struct {
		name string
		want journal.Outcome
	}{
		{name: "Matched", want: journal.OutcomeMatched},
		{name: "UNMATCHED", want: journal.OutcomeUnmatched},
		{name: "proxied", want: journal.OutcomeProxied},
		{name: "Failed", want: journal.OutcomeFailed},
		{name: "abandoned", want: journal.OutcomeAbandoned},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := journal.ParseOutcome(tt.name)
			require.Equal(t, tt.want, got)
			require.Equal(t, strings.ToLower(tt.name), got.String())
			require.True(t, got.Valid())
		})
	}
}

func TestOutcome_UnrecognizedNamesAreUnknown(t *testing.T) {
	for _, name := range []string{"", "unknown", "won", " matched", "1"} {
		t.Run(name, func(t *testing.T) {
			got := journal.ParseOutcome(name)
			require.Equal(t, journal.OutcomeUnknown, got)
			require.Equal(t, "unknown", got.String())
			require.False(t, got.Valid())
		})
	}
}

func TestEntry_ReadsBackTheJSONItWrites(t *testing.T) {
	// The admin API answers entries in the JSON of the journal's files, and a Go client reads them back into entries:
	// every field comes back as it was, a body that isn't text and the elapsed time included.
	want := entry("a", func(e *journal.Entry) {
		e.Seq, e.MockID, e.Elapsed = 7, "m1", 1500*time.Microsecond
		e.Header = map[string][]string{"Accept": {"*/*"}}
		e.Body, e.BodyTruncated = []byte{0xff, 0x00}, true
		e.Message = "why"
		e.NearMisses = []vlatte.NearMiss{{MockID: "m2", Passed: 1, Total: 2, Skipped: []string{"times"}}}
		e.Scenarios = []matchscenario.Move{{Scenario: "checkout", From: "started", To: "paid"}}
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
