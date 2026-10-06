package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// Equal matches a value equal to expected, by the rule in the package doc: numbers by their exact value whatever
// their Go types, text read as a number or a bool when compared with one, lists item by item.
// So Equal(10) matches 10.0, an int64 10 and the query value "10", but not "010".
func Equal(expected any) vlatte.Matcher {
	return &equalMatcher{Expected: expected}
}

// Equalf returns true if the matcher value is equal to the given parameter value.
func Equalf(format string, a ...any) vlatte.Matcher {
	return &equalMatcher{Expected: fmt.Sprintf(format, a...)}
}

// Eq is an alias to Equal.
func Eq(expected any) vlatte.Matcher {
	return &equalMatcher{Expected: expected}
}
