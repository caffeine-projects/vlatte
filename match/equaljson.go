package match

import (
	"github.com/caffeine-projects/vlatte"
)

// EqualJSON matches JSON values.
func EqualJSON(expected any) vlatte.Matcher {
	return &equalJSONMatcher{Expected: expected}
}

// EQJ is an alias for EqualJSON.
func EQJ(expected any) vlatte.Matcher {
	return &equalJSONMatcher{Expected: expected}
}
