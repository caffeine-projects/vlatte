package vlatte

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Layout names formatTime and parseTime take besides Go layouts: those of package time, and the numbers of Unix time.
const (
	_layoutUnix      = "unix"
	_layoutUnixMilli = "unixMilli"
)

// _namedLayouts are the layouts of package time, by name, which templates can't read from package time.
var _namedLayouts = map[string]string{
	"Layout":      time.Layout,
	"ANSIC":       time.ANSIC,
	"UnixDate":    time.UnixDate,
	"RubyDate":    time.RubyDate,
	"RFC822":      time.RFC822,
	"RFC822Z":     time.RFC822Z,
	"RFC850":      time.RFC850,
	"RFC1123":     time.RFC1123,
	"RFC1123Z":    time.RFC1123Z,
	"RFC3339":     time.RFC3339,
	"RFC3339Nano": time.RFC3339Nano,
	"Kitchen":     time.Kitchen,
	"Stamp":       time.Stamp,
	"StampMilli":  time.StampMilli,
	"StampMicro":  time.StampMicro,
	"StampNano":   time.StampNano,
	"DateTime":    time.DateTime,
	"DateOnly":    time.DateOnly,
	"TimeOnly":    time.TimeOnly,
}

// formatTime formats t with layout: a Go layout, the name of one of package time, such as RFC3339, or unix or
// unixMilli for the seconds or milliseconds of Unix time.
func formatTime(layout string, t time.Time) string {
	switch layout {
	case _layoutUnix:
		return strconv.FormatInt(t.Unix(), 10)
	case _layoutUnixMilli:
		return strconv.FormatInt(t.UnixMilli(), 10)
	}

	if named, ok := _namedLayouts[layout]; ok {
		layout = named
	}

	return t.Format(layout)
}

// parseTime parses s with layout, which is any layout formatTime takes.
func parseTime(layout, s string) (time.Time, error) {
	if layout == _layoutUnix || layout == _layoutUnixMilli {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("%q is not %s time", s, layout)
		}

		if layout == _layoutUnix {
			return time.Unix(n, 0).UTC(), nil
		}

		return time.UnixMilli(n).UTC(), nil
	}

	if named, ok := _namedLayouts[layout]; ok {
		layout = named
	}

	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, err
	}

	return t, nil
}

// _calendarUnits are the units of timeAdd that move a date: days, weeks, months and years are of the calendar, so a
// day across a daylight saving change keeps the clock time.
var _calendarUnits = map[string][3]int{
	"day": {0, 0, 1}, "week": {0, 0, 7}, "month": {0, 1, 0}, "year": {1, 0, 0},
}

// _clockUnits are the units of timeAdd that move the clock.
var _clockUnits = map[string]time.Duration{
	"second": time.Second, "minute": time.Minute, "hour": time.Hour,
}

// timeAdd moves t by offset: a Go duration, such as "1h30m", or a whole number of a unit, such as "3 days" or
// "-1 month". The units are second, minute, hour, day, week, month and year, singular or plural, in any case.
func timeAdd(offset string, t time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(offset); err == nil {
		return t.Add(d), nil
	}

	count, unit, ok := strings.Cut(strings.TrimSpace(offset), " ")
	if !ok {
		return time.Time{}, fmt.Errorf("%q is not a duration or a number of a unit, such as \"3 days\"", offset)
	}

	n, err := strconv.Atoi(count)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a whole number", count)
	}

	unit = strings.TrimSpace(unit)
	singular := strings.TrimSuffix(strings.ToLower(unit), "s")

	if d, ok := _clockUnits[singular]; ok {
		if n > math.MaxInt64/int(d) || n < math.MinInt64/int(d) {
			return time.Time{}, fmt.Errorf("%q overflows a duration", offset)
		}

		return t.Add(time.Duration(n) * d), nil
	}

	if date, ok := _calendarUnits[singular]; ok {
		for _, f := range date {
			if f > 1 && (n > math.MaxInt/f || n < math.MinInt/f) {
				return time.Time{}, fmt.Errorf("%q overflows a duration", offset)
			}
		}

		return t.AddDate(n*date[0], n*date[1], n*date[2]), nil
	}

	return time.Time{}, fmt.Errorf("unknown unit %q", unit)
}

// inZone returns t in an IANA time zone, such as "America/Sao_Paulo" or "UTC".
func inZone(zone string, t time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}

	return t.In(loc), nil
}

// _truncations are the points truncateTime takes, each returning the start of the day it names in t's year y, month
// m, day d and hour h, in t's zone.
var _truncations = map[string]func(y int, m time.Month, d, h int, loc *time.Location) time.Time{
	"first minute of hour": func(y int, m time.Month, d, h int, loc *time.Location) time.Time {
		return time.Date(y, m, d, h, 0, 0, 0, loc)
	},
	"first hour of day": func(y int, m time.Month, d, _ int, loc *time.Location) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	},
	"first day of month": func(y int, m time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y, m, 1, 0, 0, 0, 0, loc)
	},
	"first day of next month": func(y int, m time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
	},
	"last day of month": func(y int, m time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y, m+1, 0, 0, 0, 0, 0, loc)
	},
	"first day of year": func(y int, _ time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y, time.January, 1, 0, 0, 0, 0, loc)
	},
	"first day of next year": func(y int, _ time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y+1, time.January, 1, 0, 0, 0, 0, loc)
	},
	"last day of year": func(y int, _ time.Month, _, _ int, loc *time.Location) time.Time {
		return time.Date(y, time.December, 31, 0, 0, 0, 0, loc)
	},
}

// truncateTime returns the start of the hour, day, month or year point names, from t, in t's zone. The points are
// "first minute of hour", "first hour of day", "first day of month", "first day of next month", "last day of month",
// "first day of year", "first day of next year" and "last day of year".
func truncateTime(point string, t time.Time) (time.Time, error) {
	start, ok := _truncations[point]
	if !ok {
		return time.Time{}, fmt.Errorf("unknown point %q", point)
	}

	return start(t.Year(), t.Month(), t.Day(), t.Hour(), t.Location()), nil
}
