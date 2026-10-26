package match

import (
	"fmt"
	"time"

	"github.com/caffeine-projects/vlatte"
)

// Before matches a date and time before expected. The value is a [time.Time], or text: a date and time in RFC 3339,
// in ISO 8601 without a zone, or in one of the formats of HTTP dates, unless [ActualFormat] gives its layout. Other
// values, and text in none of those formats, don't match.
//
// A value without a zone is read in [time.Local] when expected has one, as WireMock reads it; [LocalTime] compares
// wall clocks instead. [ExpectedOffset], [TruncateExpected] and [TruncateActual] shift the dates and times compared.
// Truncations take the days and hours of expected's UTC offset, at expected: a named zone, such as Europe/Madrid,
// keeps its offset of that date, the one a description of the matcher writes.
func Before(expected time.Time, opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opBefore, expected, false, opts)
}

// After matches a date and time after expected. It reads values as [Before] does.
func After(expected time.Time, opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opAfter, expected, false, opts)
}

// EqualDateTime matches a date and time equal to expected: the same instant, whatever its zone. It reads values as
// [Before] does.
func EqualDateTime(expected time.Time, opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opEqual, expected, false, opts)
}

// BeforeNow matches a date and time before the time it is matched at, in UTC, as [Before] matches one before a given
// time: BeforeNow(ExpectedOffset(3, Days)) matches one before 3 days from now.
func BeforeNow(opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opBefore, time.Time{}, true, opts)
}

// AfterNow matches a date and time after the time it is matched at, in UTC, as [After] matches one after a given time.
func AfterNow(opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opAfter, time.Time{}, true, opts)
}

// EqualDateTimeNow matches a date and time equal to the time it is matched at, in UTC. It is of use with truncations:
// EqualDateTimeNow(TruncateExpected(FirstHourOfDay), TruncateActual(FirstHourOfDay)) matches any time of today, in
// UTC.
func EqualDateTimeNow(opts ...DateTimeOption) vlatte.Matcher {
	return newDateTime(_opEqual, time.Time{}, true, opts)
}

// DateTimeUnit is a unit of [ExpectedOffset].
type DateTimeUnit int

// The units of [ExpectedOffset].
const (
	Seconds DateTimeUnit = iota + 1
	Minutes
	Hours
	Days
	Weeks
	Months
	Years
)

// String returns the name of the unit as mock files write it, such as days.
func (u DateTimeUnit) String() string {
	switch u {
	case Seconds:
		return "seconds"
	case Minutes:
		return "minutes"
	case Hours:
		return "hours"
	case Days:
		return "days"
	case Weeks:
		return "weeks"
	case Months:
		return "months"
	case Years:
		return "years"
	default:
		return fmt.Sprintf("DateTimeUnit(%d)", int(u))
	}
}

// Truncation moves a date and time to the start of a period of the calendar, in its own zone, as WireMock's
// truncations do: [TruncateExpected] and [TruncateActual].
type Truncation int

// The truncations of WireMock.
const (
	FirstMinuteOfHour Truncation = iota + 1
	FirstHourOfDay
	FirstDayOfMonth
	FirstDayOfNextMonth
	LastDayOfMonth
	FirstDayOfYear
	FirstDayOfNextYear
	LastDayOfYear
)

// String returns the name of the truncation as mock files write it, such as first day of month.
func (t Truncation) String() string {
	switch t {
	case FirstMinuteOfHour:
		return "first minute of hour"
	case FirstHourOfDay:
		return "first hour of day"
	case FirstDayOfMonth:
		return "first day of month"
	case FirstDayOfNextMonth:
		return "first day of next month"
	case LastDayOfMonth:
		return "last day of month"
	case FirstDayOfYear:
		return "first day of year"
	case FirstDayOfNextYear:
		return "first day of next year"
	case LastDayOfYear:
		return "last day of year"
	default:
		return fmt.Sprintf("Truncation(%d)", int(t))
	}
}

// truncate moves t to the start of the period of the truncation. The days a truncation names start at midnight.
func (t Truncation) truncate(at time.Time) time.Time {
	y, m, d := at.Date()
	loc := at.Location()

	switch t {
	case FirstMinuteOfHour:
		return time.Date(y, m, d, at.Hour(), 0, 0, 0, loc)
	case FirstHourOfDay:
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	case FirstDayOfMonth:
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	case FirstDayOfNextMonth:
		return time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
	case LastDayOfMonth:
		return time.Date(y, m+1, 0, 0, 0, 0, 0, loc)
	case FirstDayOfYear:
		return time.Date(y, time.January, 1, 0, 0, 0, 0, loc)
	case FirstDayOfNextYear:
		return time.Date(y+1, time.January, 1, 0, 0, 0, 0, loc)
	case LastDayOfYear:
		return time.Date(y, time.December, 31, 0, 0, 0, 0, loc)
	default:
		return at
	}
}

// DateTimeOption configures the date and time matchers, such as [Before].
type DateTimeOption interface {
	applyDateTime(o *dateTimeOptions)
}

type dateTimeOptions struct {
	offset           int
	offsetUnit       DateTimeUnit
	truncateExpected Truncation
	truncateActual   Truncation
	truncateLast     bool
	actualFormat     string
	localTime        bool
}

type expectedOffset struct {
	n    int
	unit DateTimeUnit
}

func (e expectedOffset) applyDateTime(o *dateTimeOptions) { o.offset, o.offsetUnit = e.n, e.unit }

// ExpectedOffset moves the expected date and time by n units, back when n is negative, as WireMock's expectedOffset
// and "now +3 days" do. A month or a year later is the same day of the month, or the last day of a shorter month:
// January 31 and 1 month is February 28, or 29. A later ExpectedOffset replaces an earlier one.
func ExpectedOffset(n int, unit DateTimeUnit) DateTimeOption { return expectedOffset{n: n, unit: unit} }

type truncateExpected Truncation

func (t truncateExpected) applyDateTime(o *dateTimeOptions) { o.truncateExpected = Truncation(t) }

// TruncateExpected truncates the expected date and time, before [ExpectedOffset] moves it unless [TruncateLast] is
// given: AfterNow(ExpectedOffset(15, Days), TruncateExpected(FirstDayOfMonth)) matches after the 16th of this month
// begins.
func TruncateExpected(t Truncation) DateTimeOption { return truncateExpected(t) }

type truncateActual Truncation

func (t truncateActual) applyDateTime(o *dateTimeOptions) { o.truncateActual = Truncation(t) }

// TruncateActual truncates the date and time matched, in the zone of the expected one, as WireMock's truncateActual
// does: with EqualDateTime(march, TruncateActual(FirstDayOfMonth)), any time in March matches, march being March 1,
// 00:00, and March being the month in march's zone.
func TruncateActual(t Truncation) DateTimeOption { return truncateActual(t) }

type truncateLast struct{}

func (truncateLast) applyDateTime(o *dateTimeOptions) { o.truncateLast = true }

// TruncateLast makes [TruncateExpected] apply after [ExpectedOffset], as WireMock's applyTruncationLast does:
// EqualDateTimeNow(ExpectedOffset(1, Months), TruncateExpected(LastDayOfMonth), TruncateLast()) is the last day of
// next month.
func TruncateLast() DateTimeOption { return truncateLast{} }

type actualFormat string

func (f actualFormat) applyDateTime(o *dateTimeOptions) { o.actualFormat = string(f) }

// ActualFormat reads text values in layout, a Go time layout, such as "02/01/2006", rather than in the formats read
// by default. A value read with a layout that writes no zone has none. Prefer a numeric offset, such as -0700, to a
// zone abbreviation, MST: Go reads an abbreviation it doesn't know as UTC.
func ActualFormat(layout string) DateTimeOption { return actualFormat(layout) }

type localTime struct{}

func (localTime) applyDateTime(o *dateTimeOptions) { o.localTime = true }

// LocalTime makes the expected date and time a local one, without a zone, as one WireMock reads without an offset:
// its wall clock is compared with the wall clock of the value, in the value's own zone.
func LocalTime() DateTimeOption { return localTime{} }
