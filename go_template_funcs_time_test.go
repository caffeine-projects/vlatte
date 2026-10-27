package vlatte

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimeFuncs(t *testing.T) {
	at := time.Date(2026, time.March, 31, 13, 45, 30, 123_000_000, time.UTC)

	runFuncCases(t, []funcCase{
		{name: "formatTime with a named layout", text: `{{ formatTime "RFC3339" . }}`, data: at, want: "2026-03-31T13:45:30Z"},
		{name: "formatTime with RFC822", text: `{{ formatTime "RFC822" . }}`, data: at, want: "31 Mar 26 13:45 UTC"},
		{name: "formatTime with Kitchen", text: `{{ formatTime "Kitchen" . }}`, data: at, want: "1:45PM"},
		{name: "formatTime with StampMilli", text: `{{ formatTime "StampMilli" . }}`, data: at, want: "Mar 31 13:45:30.123"},
		{name: "parseTime with RFC822Z", text: `{{ parseTime "RFC822Z" "31 Mar 26 13:45 -0300" | formatTime "RFC3339" }}`, want: "2026-03-31T13:45:00-03:00"},
		{name: "formatTime with a Go layout", text: `{{ formatTime "02/01/2006 15h" . }}`, data: at, want: "31/03/2026 13h"},
		{name: "formatTime in Unix seconds", text: `{{ formatTime "unix" . }}`, data: at, want: "1774964730"},
		{name: "formatTime in Unix milliseconds", text: `{{ formatTime "unixMilli" . }}`, data: at, want: "1774964730123"},
		{name: "parseTime with a named layout", text: `{{ parseTime "RFC1123" "Tue, 31 Mar 2026 13:45:30 UTC" | formatTime "DateTime" }}`, want: "2026-03-31 13:45:30"},
		{name: "parseTime of Unix seconds", text: `{{ parseTime "unix" "1774964730" | formatTime "RFC3339" }}`, want: "2026-03-31T13:45:30Z"},
		{name: "parseTime of Unix milliseconds", text: `{{ parseTime "unixMilli" "1774964730123" | formatTime "RFC3339Nano" }}`, want: "2026-03-31T13:45:30.123Z"},
		{name: "parseTime of text that isn't Unix time", text: `{{ parseTime "unix" "soon" }}`, err: `error calling parseTime: "soon" is not unix time`},
		{name: "parseTime that doesn't fit its layout", text: `{{ parseTime "DateOnly" "31/03/2026" }}`, err: "error calling parseTime: parsing time"},
		{name: "timeAdd a Go duration", text: `{{ timeAdd "-1h30m" . | formatTime "TimeOnly" }}`, data: at, want: "12:15:30"},
		{name: "timeAdd days", text: `{{ timeAdd "3 days" . | formatTime "DateOnly" }}`, data: at, want: "2026-04-03"},
		{name: "timeAdd a unit in the singular", text: `{{ timeAdd "1 week" . | formatTime "DateOnly" }}`, data: at, want: "2026-04-07"},
		{name: "timeAdd seconds", text: `{{ timeAdd "-24 seconds" . | formatTime "TimeOnly" }}`, data: at, want: "13:45:06"},
		{
			// Months are of the calendar, as Go's AddDate counts them: March 31 plus a month is May 1.
			name: "timeAdd months", text: `{{ timeAdd "1 month" . | formatTime "DateOnly" }}`, data: at, want: "2026-05-01",
		},
		{name: "timeAdd years", text: `{{ timeAdd "-2 years" . | formatTime "DateOnly" }}`, data: at, want: "2024-03-31"},
		{name: "timeAdd a unit in any case", text: `{{ timeAdd "2 Days" . | formatTime "DateOnly" }}`, data: at, want: "2026-04-02"},
		{name: "timeAdd an overflow", text: `{{ timeAdd "9999999999 hours" . }}`, data: at, err: `error calling timeAdd: "9999999999 hours" overflows a duration`},
		{name: "timeAdd a week count that overflows", text: `{{ timeAdd "1317624576693539402 weeks" . }}`, data: at, err: `error calling timeAdd: "1317624576693539402 weeks" overflows a duration`},
		{name: "timeAdd without a unit", text: `{{ timeAdd "1 s" . }}`, data: at, err: `error calling timeAdd: unknown unit "s"`},
		{name: "timeAdd an unknown unit", text: `{{ timeAdd "3 fortnights" . }}`, data: at, err: `error calling timeAdd: unknown unit "fortnights"`},
		{name: "timeAdd a count that isn't whole", text: `{{ timeAdd "1.5 days" . }}`, data: at, err: `error calling timeAdd: "1.5" is not a whole number`},
		{name: "timeAdd text that is no offset", text: `{{ timeAdd "later" . }}`, data: at, err: `error calling timeAdd: "later" is not a duration`},
		{name: "inZone", text: `{{ inZone "America/Sao_Paulo" . | formatTime "RFC3339" }}`, data: at, want: "2026-03-31T10:45:30-03:00"},
		{name: "inZone of an unknown zone", text: `{{ inZone "Mars/Olympus" . }}`, data: at, err: "error calling inZone: unknown time zone Mars/Olympus"},
		{name: "truncateTime of an unknown point", text: `{{ truncateTime "noon" . }}`, data: at, err: `error calling truncateTime: unknown point "noon"`},
		{name: "now composes", text: `{{ now | timeAdd "1 day" | inZone "UTC" | formatTime "unix" | len }}`, want: "10"},
	})
}

func TestTruncateTime(t *testing.T) {
	at := time.Date(2026, time.February, 14, 13, 45, 30, 0, time.FixedZone("BRT", -3*60*60))

	points := []struct {
		point string
		want  string
	}{
		{point: "first minute of hour", want: "2026-02-14T13:00:00-03:00"},
		{point: "first hour of day", want: "2026-02-14T00:00:00-03:00"},
		{point: "first day of month", want: "2026-02-01T00:00:00-03:00"},
		{point: "first day of next month", want: "2026-03-01T00:00:00-03:00"},
		{point: "last day of month", want: "2026-02-28T00:00:00-03:00"},
		{point: "first day of year", want: "2026-01-01T00:00:00-03:00"},
		{point: "first day of next year", want: "2027-01-01T00:00:00-03:00"},
		{point: "last day of year", want: "2026-12-31T00:00:00-03:00"},
	}

	e := MustNewGoTemplateEngine()

	for _, tc := range points {
		t.Run(tc.point+", in the time's zone", func(t *testing.T) {
			out, err := render(t, e, `{{ truncateTime .point .at | formatTime "RFC3339" }}`, map[string]any{"point": tc.point, "at": at})
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}
