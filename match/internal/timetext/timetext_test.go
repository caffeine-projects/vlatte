package timetext

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParse_TellsZonedFromLocal(t *testing.T) {
	tests := []struct {
		text  string
		want  time.Time
		zoned bool
	}{
		{"2021-06-01T12:00:00Z", time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC), true},
		{"2021-06-01T14:00:00.5+02:00", time.Date(2021, 6, 1, 12, 0, 0, 5e8, time.UTC), true},
		{"Tue, 01 Jun 2021 12:00:00 GMT", time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC), true},
		{"2021-06-01T12:00:00", time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC), false},
		{"2021-06-01", time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC), false},
	}

	for _, tc := range tests {
		got, zoned, err := Parse(tc.text)
		require.NoError(t, err, tc.text)
		require.True(t, tc.want.Equal(got), "%s: %v", tc.text, got)
		require.Equal(t, tc.zoned, zoned, tc.text)
	}

	_, _, err := Parse("06/01/2021")
	require.EqualError(t, err, `"06/01/2021" is not a date and time in RFC 3339, ISO 8601 or an HTTP format`)
}

func TestFormat_WritesWhatParseReads(t *testing.T) {
	at := time.Date(2021, 6, 1, 12, 30, 0, 0, time.FixedZone("", 2*3600))

	for _, zoned := range []bool{true, false} {
		got, gotZoned, err := Parse(Format(at, zoned))
		require.NoError(t, err)
		require.Equal(t, zoned, gotZoned)
		require.Equal(t, at.Hour(), got.Hour(), "the wall clock")
	}
}

func TestHasZone_AnAbbreviationOrAnOffsetIsAZone(t *testing.T) {
	for layout, want := range map[string]bool{
		time.RFC3339: true, time.RFC1123: true, time.RFC822Z: true, "2006-01-02 -07": true,
		"02/01/2006": false, time.Kitchen: false, "2006-01-02T15:04:05": false,
	} {
		require.Equal(t, want, HasZone(layout), layout)
	}
}

func TestCutOffset_OnlyASignedNumberAndAUnitAtTheEnd(t *testing.T) {
	tests := []struct {
		text string
		base string
		n    int
		unit string
		ok   bool
	}{
		{"now +3 days", "now", 3, "days", true},
		{"now  -1   hour", "now", -1, "hour", true},
		{"Tue, 01 Jun 2021 12:00:00 GMT +2 weeks", "Tue, 01 Jun 2021 12:00:00 GMT", 2, "weeks", true},
		{"now", "now", 0, "", false},
		{"now 3 days", "now 3 days", 0, "", false},
		{"Tue Jun  1 12:00:00 2021", "Tue Jun  1 12:00:00 2021", 0, "", false},
		{"now +99999999999999999999 days", "now +99999999999999999999 days", 0, "", false},
	}

	for _, tc := range tests {
		base, n, unit, ok := CutOffset(tc.text)
		require.Equal(t, []any{tc.base, tc.n, tc.unit, tc.ok}, []any{base, n, unit, ok}, tc.text)
	}
}
