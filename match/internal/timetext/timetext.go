// Package timetext reads dates and times as the date and time matchers write them.
package timetext

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// _localLayouts are the ISO 8601 layouts of a local date and time: without a zone.
var _localLayouts = []string{"2006-01-02T15:04:05.999999999", "2006-01-02"}

// Parse reads a date and time in one of the formats read by default: RFC 3339, with or without a fraction of a
// second; the three formats of HTTP dates, as [http.ParseTime] reads them; or ISO 8601 without a zone, with or without
// the time. zoned reports whether text gives a zone; HTTP dates are in GMT. A local date and time is read as UTC.
func Parse(text string) (t time.Time, zoned bool, err error) {
	if t, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return t, true, nil
	}

	if t, err := http.ParseTime(text); err == nil {
		return t, true, nil
	}

	for _, layout := range _localLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t, false, nil
		}
	}

	return time.Time{}, false, fmt.Errorf("%q is not a date and time in RFC 3339, ISO 8601 or an HTTP format", text)
}

// ParseLayout reads text in layout, a Go time layout. zoned reports whether the layout writes a zone; when it doesn't,
// the date and time is read as UTC.
func ParseLayout(text, layout string) (t time.Time, zoned bool, err error) {
	t, err = time.Parse(layout, text)
	return t, HasZone(layout), err
}

// HasZone reports whether a Go time layout writes a zone: an abbreviation, MST, or an offset, such as Z07:00 or -0700.
func HasZone(layout string) bool {
	return strings.Contains(layout, "MST") || strings.Contains(layout, "Z07") || strings.Contains(layout, "-07")
}

// Format writes a date and time as Parse reads it: in RFC 3339 when zoned, or else in ISO 8601 without a zone.
func Format(t time.Time, zoned bool) string {
	if zoned {
		return t.Format(time.RFC3339Nano)
	}

	return t.Format(_localLayouts[0])
}

var _offset = regexp.MustCompile(`^(.*\S)\s+([+-][0-9]+)\s+([A-Za-z]+)$`)

// CutOffset cuts the offset off the end of text, such as "now +3 days": base is what comes before it, n the signed
// number and unit the word after it. ok is false when text doesn't end in an offset.
func CutOffset(text string) (base string, n int, unit string, ok bool) {
	m := _offset.FindStringSubmatch(text)
	if m == nil {
		return text, 0, "", false
	}

	n, err := strconv.Atoi(m[2])
	if err != nil {
		return text, 0, "", false
	}

	return m[1], n, m[3], true
}
