package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// EqualJSON matches a value whose JSON equals expected, by StrictEqual's rule. expected is JSON text,
// or a value of any type, written as JSON. A value that is text, such as a request body, is read as JSON;
// any other value is written as JSON. It panics when expected isn't JSON, or can't be written as JSON.
func EqualJSON(expected any) vlatte.Matcher {
	return &equalJSONMatcher{Expected: expected, want: mustJSON(expected)}
}

// EQJ is an alias for EqualJSON.
func EQJ(expected any) vlatte.Matcher {
	return EqualJSON(expected)
}

// mustJSON returns what expected means as JSON: JSON text, read strictly, or a value written as JSON.
func mustJSON(expected any) any {
	var (
		want any
		err  error
	)

	if text, ok := value.Text(expected); ok {
		want, err = value.DecodeJSON([]byte(text))
	} else {
		want, err = value.ViaJSON(expected)
	}

	if err != nil {
		panic(fmt.Errorf("match: eqj: %w", err))
	}

	return want
}
