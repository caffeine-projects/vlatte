package mockhttp

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Matcher = (*pathTemplate)(nil)

// pathTemplate is a path with parameters, such as /users/{id}: a parameter is a whole segment in braces, and takes any
// segment but an empty one. It splits paths at every "/", as match.RequestURI does, so a trailing "/" counts.
type pathTemplate struct {
	text     string
	segments []segment
}

// segment is a segment of a path template: a literal one, decoded, or a parameter, by name.
type segment struct {
	text  string
	param bool
}

// parsePathTemplate parses s, a path template.
func parsePathTemplate(s string) (*pathTemplate, error) {
	if !strings.HasPrefix(s, "/") {
		return nil, errors.New("a path template starts with /")
	}

	if strings.ContainsAny(s, "?#") {
		return nil, errors.New("a path template is a path, without a query or a fragment")
	}

	t := &pathTemplate{text: s}
	for part := range strings.SplitSeq(s, "/") {
		inner, opens := strings.CutPrefix(part, "{")
		name, closes := strings.CutSuffix(inner, "}")

		switch {
		case opens && closes && name == "":
			return nil, fmt.Errorf("segment %q: a parameter has a name, such as {id}", part)
		case opens && closes && !strings.ContainsAny(name, "{}"):
			if t.has(name) {
				return nil, fmt.Errorf("parameter %q is named twice", name)
			}

			t.segments = append(t.segments, segment{text: name, param: true})
		case strings.ContainsAny(part, "{}"):
			return nil, fmt.Errorf("segment %q: a parameter is a whole segment, such as {id}", part)
		default:
			decoded, err := url.PathUnescape(part)
			if err != nil {
				return nil, fmt.Errorf("segment %q: %w", part, err)
			}

			t.segments = append(t.segments, segment{text: decoded})
		}
	}

	return t, nil
}

// Name implements vlatte.Matcher.
func (t *pathTemplate) Name() string { return "URLPathTemplate" }

// Match implements vlatte.Matcher. The value is the path as the client sent it, escaped.
func (t *pathTemplate) Match(arg vlatte.MatcherArg) (bool, error) {
	path, _ := arg.Value().(string)
	_, ok := t.params(path)

	return ok, nil
}

// params returns the parameters of escaped, a path as the client sent it, decoded, and whether the path fits t: as
// many segments, each literal one the same once decoded, and a non-empty one for each parameter.
func (t *pathTemplate) params(escaped string) (map[string]string, bool) {
	parts := strings.Split(escaped, "/")
	if len(parts) != len(t.segments) {
		return nil, false
	}

	params := make(map[string]string)
	for i, part := range parts {
		// A segment that doesn't decode stays as it is, as in URLPathSegments.
		if decoded, err := url.PathUnescape(part); err == nil {
			part = decoded
		}

		switch s := t.segments[i]; {
		case !s.param:
			if part != s.text {
				return nil, false
			}
		case part == "":
			return nil, false
		default:
			params[s.text] = part
		}
	}

	return params, true
}

// has reports whether t has the parameter name.
func (t *pathTemplate) has(name string) bool {
	return slices.Contains(t.segments, segment{text: name, param: true})
}

// paramNames returns the names of the parameters of t, in path order.
func (t *pathTemplate) paramNames() []string {
	var names []string
	for _, s := range t.segments {
		if s.param {
			names = append(names, s.text)
		}
	}

	return names
}
