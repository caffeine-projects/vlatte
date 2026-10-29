package match

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func dateMatches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()

	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

var _june1 = time.Date(2021, time.June, 1, 12, 0, 0, 0, time.UTC)

func TestDateTime_ComparesInstants(t *testing.T) {
	tests := []struct {
		name  string
		m     vlatte.Matcher
		value any
		want  bool
	}{
		{"before", Before(_june1), "2021-06-01T11:59:59Z", true},
		{"not before itself", Before(_june1), "2021-06-01T12:00:00Z", false},
		{"after", After(_june1), "2021-06-01T12:00:00.001Z", true},
		{"not after", After(_june1), "2021-05-31T12:00:00Z", false},
		{"equal", EqualDateTime(_june1), "2021-06-01T12:00:00Z", true},
		{"equal in another zone", EqualDateTime(_june1), "2021-06-01T14:00:00+02:00", true},
		{"not equal", EqualDateTime(_june1), "2021-06-01T12:00:01Z", false},
		{"a time.Time", EqualDateTime(_june1), _june1.In(time.FixedZone("x", 3600)), true},
		{"bytes", EqualDateTime(_june1), []byte("2021-06-01T12:00:00Z"), true},
		{"HTTP date", EqualDateTime(_june1), "Tue, 01 Jun 2021 12:00:00 GMT", true},
		{"RFC 850 date", EqualDateTime(_june1), "Tuesday, 01-Jun-21 12:00:00 GMT", true},
		{"asctime date", EqualDateTime(_june1), "Tue Jun  1 12:00:00 2021", true},
		{"date only", Before(_june1), "2021-06-01", true},
		{"not a date", Before(_june1), "yesterday", false},
		{"not text", Before(_june1), 5, false},
		{"nil", Before(_june1), nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dateMatches(t, tc.m, tc.value))
		})
	}
}

func TestDateTime_LocalAndZoned(t *testing.T) {
	// A value without a zone is read in the system's zone when the expected one has a zone, and a local
	// expected date and time compares wall clocks, whatever the value's zone. The system's zone here isn't UTC, as it
	// is on CI's machines, where reading the value as UTC would pass too.
	system := time.Local
	time.Local = time.FixedZone("system", 5*3600)
	t.Cleanup(func() { time.Local = system })

	noon := time.Date(2021, time.June, 1, 12, 0, 0, 0, time.UTC)
	require.True(t, dateMatches(t, EqualDateTime(noon), "2021-06-01T17:00:00"))
	require.False(t, dateMatches(t, EqualDateTime(noon), "2021-06-01T12:00:00"))

	wall := time.Date(2021, time.June, 1, 12, 0, 0, 0, time.UTC)
	require.True(t, dateMatches(t, EqualDateTime(wall, LocalTime()), "2021-06-01T12:00:00+05:00"))
	require.True(t, dateMatches(t, EqualDateTime(wall, LocalTime()), "2021-06-01T12:00:00"))
	require.False(t, dateMatches(t, EqualDateTime(wall, LocalTime()), "2021-06-01T13:00:00+01:00"))
}

func TestDateTime_ActualFormat(t *testing.T) {
	m := EqualDateTime(time.Date(2021, time.June, 24, 0, 0, 0, 0, time.UTC), ActualFormat("02/01/2006"), LocalTime())
	require.True(t, dateMatches(t, m, "24/06/2021"))
	require.False(t, dateMatches(t, m, "2021-06-24T00:00:00Z"), "only the layout given")

	zoned := Before(_june1, ActualFormat("2006-01-02 15:04 MST"))
	require.True(t, dateMatches(t, zoned, "2021-06-01 11:00 UTC"))
}

func TestDateTime_Truncations(t *testing.T) {
	at := time.Date(2021, time.February, 10, 13, 45, 30, 5, time.UTC)

	tests := []struct {
		truncation Truncation
		want       time.Time
	}{
		{FirstMinuteOfHour, time.Date(2021, time.February, 10, 13, 0, 0, 0, time.UTC)},
		{FirstHourOfDay, time.Date(2021, time.February, 10, 0, 0, 0, 0, time.UTC)},
		{FirstDayOfMonth, time.Date(2021, time.February, 1, 0, 0, 0, 0, time.UTC)},
		{FirstDayOfNextMonth, time.Date(2021, time.March, 1, 0, 0, 0, 0, time.UTC)},
		{LastDayOfMonth, time.Date(2021, time.February, 28, 0, 0, 0, 0, time.UTC)},
		{FirstDayOfYear, time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{FirstDayOfNextYear, time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{LastDayOfYear, time.Date(2021, time.December, 31, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		t.Run(tc.truncation.String(), func(t *testing.T) {
			require.True(t, dateMatches(t, EqualDateTime(at, TruncateExpected(tc.truncation)), tc.want))
			require.True(t, dateMatches(t, EqualDateTime(tc.want, TruncateActual(tc.truncation)), at))
		})
	}
}

func TestDateTime_TruncateActualMatchesAPeriod(t *testing.T) {
	// "Is in March 2020".
	march := EqualDateTime(time.Date(2020, time.March, 1, 0, 0, 0, 0, time.UTC), TruncateActual(FirstDayOfMonth))
	require.True(t, dateMatches(t, march, "2020-03-31T23:59:59Z"))
	require.True(t, dateMatches(t, march, "2020-03-01T00:00:00Z"))
	require.False(t, dateMatches(t, march, "2020-04-01T00:00:00Z"))
}

func TestDateTime_ExpectedOffset(t *testing.T) {
	jan31 := time.Date(2020, time.January, 31, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		n    int
		unit DateTimeUnit
		want time.Time
	}{
		{90, Seconds, jan31.Add(90 * time.Second)},
		{-5, Minutes, jan31.Add(-5 * time.Minute)},
		{3, Hours, jan31.Add(3 * time.Hour)},
		{2, Days, time.Date(2020, time.February, 2, 10, 0, 0, 0, time.UTC)},
		{1, Weeks, time.Date(2020, time.February, 7, 10, 0, 0, 0, time.UTC)},
		// A shorter month keeps its last day, as java.time does, rather than run into the next.
		{1, Months, time.Date(2020, time.February, 29, 10, 0, 0, 0, time.UTC)},
		{-2, Months, time.Date(2019, time.November, 30, 10, 0, 0, 0, time.UTC)},
		{1, Years, time.Date(2021, time.January, 31, 10, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		t.Run(tc.unit.String(), func(t *testing.T) {
			require.True(t, dateMatches(t, EqualDateTime(jan31, ExpectedOffset(tc.n, tc.unit)), tc.want))
		})
	}

	leap := time.Date(2020, time.February, 29, 0, 0, 0, 0, time.UTC)
	require.True(t, dateMatches(t, EqualDateTime(leap, ExpectedOffset(1, Years)), "2021-02-28T00:00:00Z"))
}

func TestDateTime_TruncateLast(t *testing.T) {
	// The offset follows the truncation, unless TruncateLast puts it first.
	jan30 := time.Date(2021, time.January, 30, 9, 0, 0, 0, time.UTC)

	first := EqualDateTime(jan30, ExpectedOffset(5, Days), TruncateExpected(FirstDayOfMonth))
	require.True(t, dateMatches(t, first, "2021-01-06T00:00:00Z"))

	last := EqualDateTime(jan30, ExpectedOffset(5, Days), TruncateExpected(FirstDayOfMonth), TruncateLast())
	require.True(t, dateMatches(t, last, "2021-02-01T00:00:00Z"))
}

func TestDateTime_NowIsTheTimeOfTheMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now().UTC()
		inTwoDays := start.Add(48 * time.Hour).Format(time.RFC3339)

		withinThree := BeforeNow(ExpectedOffset(3, Days))
		require.True(t, dateMatches(t, withinThree, inTwoDays))
		require.False(t, dateMatches(t, withinThree, start.Add(96*time.Hour).Format(time.RFC3339)))
		require.True(t, dateMatches(t, AfterNow(), inTwoDays))

		// Now moves: three days on, the same value is in the past.
		time.Sleep(72 * time.Hour)
		require.True(t, dateMatches(t, BeforeNow(), inTwoDays))
		require.False(t, dateMatches(t, AfterNow(), inTwoDays))

		today := EqualDateTimeNow(TruncateExpected(FirstHourOfDay), TruncateActual(FirstHourOfDay))
		require.True(t, dateMatches(t, today, time.Now().UTC().Format(time.RFC3339)))
		require.False(t, dateMatches(t, today, time.Now().UTC().Add(24*time.Hour).Format(time.RFC3339)))

		// Today is a day of the expected zone, UTC: a value in another zone is truncated in that zone too.
		require.True(t, dateMatches(t, today, time.Now().In(time.FixedZone("", 2*3600)).Format(time.RFC3339)))
	})
}

func TestDateTime_TruncatesInTheOffsetOfTheExpectedTime(t *testing.T) {
	// A description writes the expected time with its UTC offset, which is all a mock file loads back: a named zone
	// truncates with its offset at the expected time, whatever the offset on the other side of a change to summer
	// time, so a matcher built in Go and the one loaded from its description agree.
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	// March 30 is in summer time, +02:00; its first day of the month, March 1, is in winter time, +01:00.
	m := Before(time.Date(2026, time.March, 30, 10, 0, 0, 0, madrid), TruncateExpected(FirstDayOfMonth))
	require.True(t, dateMatches(t, m, "2026-02-28T21:59:00Z"))
	require.False(t, dateMatches(t, m, "2026-02-28T22:30:00Z"), "midnight of March 1 at +02:00 is 22:00Z")
}

func TestDateTime_Describes(t *testing.T) {
	tests := []struct {
		m    vlatte.Matcher
		want string
	}{
		{Before(_june1), `["before", "2021-06-01T12:00:00Z"]`},
		{After(_june1, LocalTime()), `["after", "2021-06-01T12:00:00"]`},
		{EqualDateTime(_june1, ExpectedOffset(-3, Days)), `["equalDateTime", "2021-06-01T12:00:00Z -3 days"]`},
		{BeforeNow(ExpectedOffset(2, Months)), `["before", "now +2 months"]`},
		{
			AfterNow(TruncateExpected(FirstDayOfMonth), TruncateActual(LastDayOfYear), TruncateLast(), ActualFormat("02/01/2006"), LocalTime()),
			`["after", "now", {"truncate_expected": "first day of month", "truncate_actual": "last day of year", "truncate_last": true, "actual_format": "02/01/2006", "local_time": true}]`,
		},
	}

	for _, tc := range tests {
		data, err := tc.m.(vlatte.SelfDescriptive).Describe().MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(data))
	}
}

func TestDateTime_UnitsAndTruncationsAreNamedAsMockFilesWriteThem(t *testing.T) {
	// A description writes these names, and a mock file reads them back.
	units := map[DateTimeUnit]string{
		Seconds: "seconds", Minutes: "minutes", Hours: "hours", Days: "days", Weeks: "weeks", Months: "months",
		Years: "years", DateTimeUnit(99): "DateTimeUnit(99)",
	}
	for unit, want := range units {
		require.Equal(t, want, unit.String())
	}

	truncations := map[Truncation]string{
		FirstMinuteOfHour: "first minute of hour", FirstHourOfDay: "first hour of day", FirstDayOfMonth: "first day of month",
		FirstDayOfNextMonth: "first day of next month", LastDayOfMonth: "last day of month",
		FirstDayOfYear: "first day of year", FirstDayOfNextYear: "first day of next year", LastDayOfYear: "last day of year",
		Truncation(99): "Truncation(99)",
	}
	for truncation, want := range truncations {
		require.Equal(t, want, truncation.String())
	}
}

func TestDateTime_AnUnknownTruncationOrUnitChangesNothing(t *testing.T) {
	m := EqualDateTime(_june1, TruncateExpected(Truncation(99)), ExpectedOffset(3, DateTimeUnit(99)))
	require.True(t, dateMatches(t, m, "2021-06-01T12:00:00Z"))
}
