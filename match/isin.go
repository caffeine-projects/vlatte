package match

import (
	"github.com/caffeine-projects/vlatte"
)

// IsIn matches a value equal to one of items, by Equal's rule, so IsIn([]int{1, 2}) matches the query value "2".
func IsIn[T any](items []T) vlatte.Matcher {
	return &isInMatcher{Items: anys(items)}
}
