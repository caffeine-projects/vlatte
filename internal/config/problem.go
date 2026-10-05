package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Codes of problems. They are stable, so tools can act on them.
const (
	CodeUnknownKey         = "unknown_key"
	CodeInvalidValue       = "invalid_value"
	CodeMissingValue       = "missing_value"
	CodeConflict           = "conflict"
	CodeNotAllowed         = "not_allowed"
	CodeFileNotFound       = "file_not_found"
	CodeFileAmbiguous      = "file_ambiguous"
	CodeUnsupportedFormat  = "unsupported_format"
	CodeParseError         = "parse_error"
	CodeReadError          = "read_error"
	CodeUnexpectedArgument = "unexpected_argument"
)

// Problem is one invalid setting: what is wrong, where it came from, and how to fix it.
type Problem struct {
	Code    string // stable identifier, such as "unknown_key"
	Origin  string // where it happened, such as "env VLATTE_ADDR" or "file .vlatterc.json:3:7"
	Key     string // the setting's key, when known
	Value   string // the offending value, when there is one
	Message string // what is wrong
	Hint    string // how to fix it, when known
}

// String formats the problem as "<origin>: <code>: <message>[; <hint>]".
func (p Problem) String() string {
	s := p.Origin + ": " + p.Code + ": " + p.Message
	if p.Hint != "" {
		s += "; " + p.Hint
	}

	return s
}

// Error lists every problem found while loading settings.
type Error struct {
	Problems []Problem
}

// Error formats a header line followed by one line per problem, sorted by origin and key.
func (e *Error) Error() string {
	problems := slices.Clone(e.Problems)
	sortProblems(problems)

	var b strings.Builder
	fmt.Fprintf(&b, "config: %d problem", len(problems))
	if len(problems) != 1 {
		b.WriteString("s")
	}

	for _, p := range problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}

	return b.String()
}

func sortProblems(problems []Problem) {
	slices.SortStableFunc(problems, func(a, b Problem) int {
		return cmp.Or(strings.Compare(a.Origin, b.Origin), strings.Compare(a.Key, b.Key))
	})
}

// suggest returns the candidate closest to name, or "" when none is close enough to be a typo of it.
func suggest(name string, candidates []string) string {
	best, bestDistance := "", 0
	for _, candidate := range candidates {
		d := distance(name, candidate)
		if d > max(2, len(name)/4) {
			continue
		}

		if best == "" || d < bestDistance || (d == bestDistance && candidate < best) {
			best, bestDistance = candidate, d
		}
	}

	return best
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}

		prev, cur = cur, prev
	}

	return prev[len(b)]
}

// display hides the password of a URL, such as a proxy URL, before it goes into a message.
// It doesn't parse the URL: a value in a message is often invalid, and its password must stay hidden too.
// The password follows the user name, after the scheme if there is one, up to the last @.
func display(s string) string {
	at := strings.LastIndex(s, "@")
	if at < 0 {
		return s
	}

	start := 0
	if i := strings.Index(s[:at], "://"); i >= 0 {
		start = i + len("://")
	}

	colon := strings.Index(s[start:at], ":")
	if colon < 0 {
		return s
	}

	return s[:start+colon+1] + "xxxxx" + s[at:]
}
