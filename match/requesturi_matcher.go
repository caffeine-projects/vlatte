package match

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.Matcher             = (*requestURIMatcher)(nil)
	_ descriptor.SelfDescriptive = (*requestURIMatcher)(nil)
)

// requestURIMatcher passes when the value is a URL with the path and query of Expected.
type requestURIMatcher struct {
	Expected string
	want     requestTarget
}

// Name implements vlatte.Matcher.
func (m *requestURIMatcher) Name() string { return "RequestURI" }

// Match implements vlatte.Matcher. A value that isn't a URL is a mismatch, never an error.
func (m *requestURIMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	u, ok := toURL(arg.Value())
	if !ok {
		return mismatch("RequestURI() Value is not a URL")
	}

	if m.want.matches(u) {
		return success()
	}

	return mismatch(strings.Join([]string{"RequestURI(", m.Expected, ") ", u.RequestURI()}, ""))
}

// Describe implements descriptor.SelfDescriptive.
func (m *requestURIMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("requestURI", m.Expected)
}

// requestTarget is what RequestURI compares of a URL: its decoded path segments, its query and its fragment.
type requestTarget struct {
	segments []string
	query    url.Values // nil when the raw query doesn't parse: rawQuery compares then
	rawQuery string
	fragment string
	hasFrag  bool
}

func newRequestTarget(u *url.URL) requestTarget {
	t := requestTarget{
		segments: segments(escapedPath(u)),
		rawQuery: u.RawQuery,
		fragment: u.Fragment,
		hasFrag:  u.Fragment != "" || u.RawFragment != "",
	}

	if q, err := url.ParseQuery(u.RawQuery); err == nil {
		t.query = q
	}

	return t
}

// matches reports whether u has the path and query of t, and its fragment when t has one. The path goes first: it
// tells most requests apart, without parsing their query.
func (t requestTarget) matches(u *url.URL) bool {
	if !sameSegments(t.segments, escapedPath(u)) {
		return false
	}

	if t.hasFrag && t.fragment != u.Fragment {
		return false
	}

	// A query that doesn't parse compares as text: the same text parses the same way, so one that parses and one
	// that doesn't differ.
	if t.query == nil {
		return t.rawQuery == u.RawQuery
	}

	got, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}

	return maps.EqualFunc(t.query, got, slices.Equal)
}

// escapedPath is the path of u, escaped, and "/" when empty, as in a request.
func escapedPath(u *url.URL) string {
	if path := u.EscapedPath(); path != "" {
		return path
	}

	return "/"
}

// segments splits an escaped path at its "/" and decodes each segment: an escaped "%2F" stays within its segment.
// A segment that doesn't decode stays as it is.
func segments(escaped string) []string {
	parts := strings.Split(escaped, "/")
	for i, part := range parts {
		parts[i] = decodeSegment(part)
	}

	return parts
}

// sameSegments reports whether escaped has the segments of want, decoding them one by one up to the first that differs.
func sameSegments(want []string, escaped string) bool {
	i := 0
	for part := range strings.SplitSeq(escaped, "/") {
		if i == len(want) || decodeSegment(part) != want[i] {
			return false
		}

		i++
	}

	return i == len(want)
}

// decodeSegment decodes a path segment, or returns it as it is when it doesn't decode.
func decodeSegment(part string) string {
	if decoded, err := url.PathUnescape(part); err == nil {
		return decoded
	}

	return part
}

// toURL returns the URL a value names: a *url.URL or url.URL, text that parses as one, or a value whose String does.
func toURL(v any) (*url.URL, bool) {
	switch e := v.(type) {
	case *url.URL:
		return e, e != nil
	case url.URL:
		return &e, true
	case fmt.Stringer:
		v = e.String()
	}

	text, ok := value.Text(v)
	if !ok {
		return nil, false
	}

	u, err := parseTarget(text)
	if err != nil {
		return nil, false
	}

	return u, true
}

// parseTarget parses a URL, or the target of a request, such as "/items?page=2" or "//a": text that starts with "/" is
// a path, never a host. It accepts what url.Parse accepts.
func parseTarget(s string) (*url.URL, error) {
	if !strings.HasPrefix(s, "/") {
		return url.Parse(s)
	}

	rest, fragment, hasFragment := strings.Cut(s, "#")

	u, err := url.ParseRequestURI(rest)
	if err != nil {
		return nil, err
	}

	if hasFragment {
		f, err := url.Parse("#" + fragment)
		if err != nil {
			return nil, err
		}

		u.Fragment, u.RawFragment = f.Fragment, f.RawFragment
	}

	return u, nil
}
