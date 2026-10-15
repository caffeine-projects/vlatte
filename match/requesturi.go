package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

// RequestURI matches a URL whose path and query are those of expected, such as "/items?page=2": the same path, and
// the same query parameters, each with the same values in the same order, though the parameters can come in any order.
// Paths compare segment by segment once decoded, so "%41" is "A", but an escaped "%2F" is not a "/". A fragment counts
// only when expected has one. The scheme and the host don't count. The value is a URL, or text that parses as one.
// It panics when expected isn't a URL.
func RequestURI(expected string) vlatte.Matcher {
	u, err := parseTarget(expected)
	if err != nil {
		panic(fmt.Errorf("match: requesturi: %w", err))
	}

	return &requestURIMatcher{Expected: expected, want: newRequestTarget(u)}
}
