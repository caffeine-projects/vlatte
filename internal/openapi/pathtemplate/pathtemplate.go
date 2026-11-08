// Package pathtemplate reads the paths of an OpenAPI document, whose segments may hold parameters, such as {id}. The
// mock generator and the openapi matcher read them alike: the matcher routes a request to the operation whose mocks
// the generator orders first.
package pathtemplate

import (
	"net/url"
	"regexp"
	"strings"
)

// Param matches a parameter of a path, such as {id}.
var Param = regexp.MustCompile(`\{[^{}/]+\}`)

// _wholeParam matches a segment that is a parameter, such as {id}.
var _wholeParam = regexp.MustCompile(`^\{[^{}/]+\}$`)

// Templated returns the number of segments of path that hold a parameter. Of two paths that match a request, OpenAPI
// takes the one with fewer, as /pets/mine before /pets/{id}.
func Templated(path string) int {
	n := 0
	for seg := range strings.SplitSeq(path, "/") {
		if Param.MatchString(seg) {
			n++
		}
	}

	return n
}

// Kind is how the mocks of a path compare a request's path.
type Kind int

const (
	// Literal compares the request's path, decoded, with [Route.Path], for a path without parameters.
	Literal Kind = iota

	// Segments compares the request's path with [Route.Path], a path template whose parameters are whole segments:
	// as a mock's path_template does it, both split at each / before they are decoded.
	Segments

	// Pattern matches the request's path, decoded, against [Route.Path], a regular expression, for a parameter inside
	// a segment, such as {name}.txt.
	Pattern
)

// Route is where the mocks of a path answer: how they compare a request's path, and with what.
type Route struct {
	// Kind is how the mocks compare a request's path.
	Kind Kind

	// Path is what they compare it with: the path decoded, a path template, or a regular expression.
	Path string

	segments []segment      // the segments of Path, for Segments
	pattern  *regexp.Regexp // Path with a group for each parameter, for Pattern
	names    []string       // the names of the parameters of pattern, in order
}

// segment is a segment of a path template: a literal one, decoded, or a parameter, by name.
type segment struct {
	text  string
	param bool
}

// NewRoute returns the route of path, a path of the spec under base, a base path decoded. An escape of the spec, such
// as %20, matches the request that sends it.
func NewRoute(base, path string) *Route {
	// A glob would read a * or [ of the path as its own.
	if !Param.MatchString(path) {
		return &Route{Kind: Literal, Path: Join(base, Decode(path))}
	}

	whole := true
	for seg := range strings.SplitSeq(path, "/") {
		if strings.ContainsAny(seg, "{}") && !_wholeParam.MatchString(seg) {
			whole = false
		}
	}

	if whole {
		return template(Join((&url.URL{Path: base}).EscapedPath(), escapePercent(path)))
	}

	quoted := func(s string) string { return regexp.QuoteMeta(Decode(s)) }
	prefix := "^" + regexp.QuoteMeta(strings.TrimSuffix(base, "/"))
	rt := &Route{Kind: Pattern, Path: prefix + Fill(path, quoted, func(string) string { return `[^/]+` }) + "$"}
	rt.pattern = regexp.MustCompile(prefix + Fill(path, quoted, func(param string) string {
		rt.names = append(rt.names, param[1:len(param)-1])
		return `([^/]+)`
	}) + "$")

	return rt
}

// NewExample returns the route of an example of path, a path of the spec under base, whose parameters, such as {id},
// take the values of values, by parameter, decoded: a parameter values lacks stays one. Its mocks match only requests
// that those of [NewRoute] match: the ones with those values, or with others for a parameter inside a segment, whose
// example matches the same path decoded. It returns false when they would match none: a value is empty, or holds a /
// inside a segment.
func NewExample(base, path string, values map[string]string) (*Route, bool) {
	rt := NewRoute(base, path)

	ok := true
	fill := func(escape func(v string) string) func(param string) string {
		return func(param string) string {
			v, found := values[param]
			if !found {
				return param
			}

			ok = ok && v != ""

			return escape(v)
		}
	}

	switch rt.Kind {
	case Literal:
		return rt, true
	case Segments:
		// Escaped as a path, but for its /: a value is one segment.
		escaped := fill(func(v string) string { return strings.ReplaceAll((&url.URL{Path: v}).EscapedPath(), "/", "%2F") })
		ex := template(Join((&url.URL{Path: base}).EscapedPath(), Fill(path, escapePercent, escaped)))

		return ex, ok
	default:
		ex := &Route{Kind: Literal, Path: Join(base, Fill(path, Decode, fill(func(v string) string { return v })))}

		return ex, ok && rt.pattern.MatchString(ex.Path)
	}
}

// template returns the route of path, a path template whose parameters are whole segments, its literal segments
// escaped.
func template(path string) *Route {
	rt := &Route{Kind: Segments, Path: path}
	for seg := range strings.SplitSeq(path, "/") {
		if _wholeParam.MatchString(seg) {
			rt.segments = append(rt.segments, segment{text: seg[1 : len(seg)-1], param: true})
		} else {
			rt.segments = append(rt.segments, segment{text: Decode(seg)})
		}
	}

	return rt
}

// Match returns the values of the parameters of rt in the path of u, a request's URL, decoded, and whether the mocks
// of rt match it. A parameter takes any text but an empty one, and a slash only sent as %2F in a whole segment.
func (rt *Route) Match(u *url.URL) (map[string]string, bool) {
	switch rt.Kind {
	case Literal:
		return nil, u.Path == rt.Path
	case Segments:
		parts := strings.Split(u.EscapedPath(), "/")
		if len(parts) != len(rt.segments) {
			return nil, false
		}

		params := make(map[string]string)
		for i, part := range parts {
			part = Decode(part)

			switch s := rt.segments[i]; {
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
	default:
		values := rt.pattern.FindStringSubmatch(u.Path)
		if values == nil {
			return nil, false
		}

		params := make(map[string]string, len(rt.names))
		for i, name := range rt.names {
			params[name] = values[i+1]
		}

		return params, true
	}
}

// Join joins base, a base path, and path, a path under it.
func Join(base, path string) string {
	return strings.TrimSuffix(base, "/") + path
}

// Fill returns path, a path of the spec, with literal applied to each text between its parameters and each
// parameter, such as {id}, replaced by param of it.
func Fill(path string, literal, param func(s string) string) string {
	var b strings.Builder

	last := 0
	for _, loc := range Param.FindAllStringIndex(path, -1) {
		b.WriteString(literal(path[last:loc[0]]))
		b.WriteString(param(path[loc[0]:loc[1]]))

		last = loc[1]
	}

	b.WriteString(literal(path[last:]))

	return b.String()
}

// Decode returns s, text of a path of the spec, decoded, as the server compares a path: a % that starts no escape,
// such as that of 50%, is a percent sign.
func Decode(s string) string {
	// Every % starts an escape once escapePercent is done.
	d, _ := url.PathUnescape(escapePercent(s))

	return d
}

// escapePercent returns s, text of a path of the spec, with each % that starts no escape, such as that of 50%, escaped
// as %25: a percent sign.
func escapePercent(s string) string {
	var b strings.Builder

	for i := range len(s) {
		b.WriteByte(s[i])

		if s[i] == '%' && (i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2])) {
			b.WriteString("25")
		}
	}

	return b.String()
}

// isHex reports whether c is a hexadecimal digit.
func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}
