package mockgrpc

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Matcher = methodName("")

// methodName matches a full method, such as "/pkg.Service/Method", by a name of whole segments: the full method, or
// its end after a "/" or a ".", such as "Method", "Service/Method" or "pkg.Service/Method". "Meth" doesn't match.
type methodName string

// Name implements vlatte.Matcher.
func (n methodName) Name() string { return "Method" }

// Match implements vlatte.Matcher.
func (n methodName) Match(arg vlatte.MatcherArg) (bool, error) {
	full, _ := arg.Value().(string)
	rest, ok := strings.CutSuffix(full, string(n))

	return ok && (rest == "" || strings.HasSuffix(rest, "/") || strings.HasSuffix(rest, ".")), nil
}
