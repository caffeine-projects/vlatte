package match

import (
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
	"github.com/caffeine-projects/vlatte/match/internal/timetext"
)

var (
	_ vlatte.Matcher             = (*dateTimeMatcher)(nil)
	_ descriptor.SelfDescriptive = (*dateTimeMatcher)(nil)
)

// dateTimeOp is how a date and time matcher compares the value with the expected date and time.
type dateTimeOp int

const (
	_opBefore dateTimeOp = iota + 1
	_opAfter
	_opEqual
)

// dateTimeMatcher passes when the date and time of the value compares with the expected one as op says.
type dateTimeMatcher struct {
	op       dateTimeOp
	Expected time.Time
	now      bool // whether the expected date and time is the time of the match
	opts     dateTimeOptions
}

func newDateTime(op dateTimeOp, expected time.Time, now bool, opts []DateTimeOption) *dateTimeMatcher {
	m := &dateTimeMatcher{op: op, Expected: expected, now: now}
	for _, opt := range opts {
		opt.applyDateTime(&m.opts)
	}

	return m
}

// Name implements vlatte.Matcher.
func (m *dateTimeMatcher) Name() string {
	switch m.op {
	case _opBefore:
		return "Before"
	case _opAfter:
		return "After"
	default:
		return "EqualDateTime"
	}
}

// Match implements vlatte.Matcher.
func (m *dateTimeMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	got, zoned, ok := m.actual(v)
	if !ok {
		return mismatch(strings.Join([]string{m.Name(), "() Value is not a date and time: ", anyconv.Stringify(v)}, ""))
	}

	want := m.expected()

	switch {
	case m.opts.localTime:
		want, got = wallClock(want), wallClock(got)
	case !zoned:
		got = time.Date(got.Year(), got.Month(), got.Day(), got.Hour(), got.Minute(), got.Second(), got.Nanosecond(), time.Local)
	}

	if m.opts.truncateActual != 0 {
		got = m.opts.truncateActual.truncate(got)
	}

	var passed bool

	switch m.op {
	case _opBefore:
		passed = got.Before(want)
	case _opAfter:
		passed = got.After(want)
	default:
		passed = got.Equal(want)
	}

	if passed {
		return success()
	}

	return mismatch(strings.Join([]string{m.Name(), "(", want.Format(time.RFC3339Nano), ") Got: ", got.Format(time.RFC3339Nano)}, ""))
}

// offset moves t by n units. Months and years keep the day of the month, or take the last day of a shorter month.
func offset(t time.Time, n int, unit DateTimeUnit) time.Time {
	switch unit {
	case Seconds:
		return t.Add(time.Duration(n) * time.Second)
	case Minutes:
		return t.Add(time.Duration(n) * time.Minute)
	case Hours:
		return t.Add(time.Duration(n) * time.Hour)
	case Days:
		return t.AddDate(0, 0, n)
	case Weeks:
		return t.AddDate(0, 0, 7*n)
	case Months:
		return addMonths(t, n)
	case Years:
		return addMonths(t, 12*n)
	default:
		return t
	}
}

func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	last := time.Date(y, m+time.Month(n)+1, 0, 0, 0, 0, 0, t.Location()).Day()

	return time.Date(y, m+time.Month(n), min(d, last), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// wallClock returns the date and time t shows, in its own zone, as the same date and time in UTC.
func wallClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// Describe implements descriptor.SelfDescriptive: the expected date and time as mock files write it, now or a date and
// time, with its offset, such as "now +3 days", and the other options as an object after it.
func (m *dateTimeMatcher) Describe() descriptor.Descriptor {
	key := map[dateTimeOp]string{_opBefore: "before", _opAfter: "after", _opEqual: "equalDateTime"}[m.op]

	expected := "now"
	if !m.now {
		expected = timetext.Format(m.Expected, !m.opts.localTime)
	}

	if m.opts.offsetUnit != 0 {
		sign := "+"
		if m.opts.offset < 0 {
			sign = "-"
		}

		expected += " " + sign + strconv.Itoa(abs(m.opts.offset)) + " " + m.opts.offsetUnit.String()
	}

	opts := make(map[string]any)
	if m.opts.truncateExpected != 0 {
		opts["truncate_expected"] = m.opts.truncateExpected.String()
	}

	if m.opts.truncateActual != 0 {
		opts["truncate_actual"] = m.opts.truncateActual.String()
	}

	if m.opts.truncateLast {
		opts["truncate_last"] = true
	}

	if m.opts.actualFormat != "" {
		opts["actual_format"] = m.opts.actualFormat
	}

	if m.opts.localTime && m.now {
		opts["local_time"] = true
	}

	if len(opts) == 0 {
		return descriptor.SingleArg(key, expected)
	}

	return descriptor.MultiArgs(key, expected, opts)
}

// actual reads the date and time of a value. zoned reports whether it has a zone.
func (m *dateTimeMatcher) actual(v any) (t time.Time, zoned, ok bool) {
	if t, isTime := v.(time.Time); isTime {
		return t, true, true
	}

	text, isText := value.Text(v)
	if !isText {
		return time.Time{}, false, false
	}

	var err error

	text = strings.TrimSpace(text)
	if m.opts.actualFormat != "" {
		t, zoned, err = timetext.ParseLayout(text, m.opts.actualFormat)
	} else {
		t, zoned, err = timetext.Parse(text)
	}

	return t, zoned, err == nil
}

// expected returns the expected date and time, truncated and moved by the offset.
func (m *dateTimeMatcher) expected() time.Time {
	t := m.Expected
	if m.now {
		t = time.Now().UTC()
	}

	if m.opts.truncateExpected != 0 && !m.opts.truncateLast {
		t = m.opts.truncateExpected.truncate(t)
	}

	t = offset(t, m.opts.offset, m.opts.offsetUnit)

	if m.opts.truncateExpected != 0 && m.opts.truncateLast {
		t = m.opts.truncateExpected.truncate(t)
	}

	return t
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}
