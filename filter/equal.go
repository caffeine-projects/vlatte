package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Equal asserts that the given expectation is equal to the incoming request value.
// It considers equivalent value. Eg.: float64(10) is equal to int(10).
func Equal(expected any) *filtertype.Equal {
	return &filtertype.Equal{Expected: expected}
}

// Equalf returns true if the matcher value is equal to the given parameter value.
func Equalf(format string, a ...any) *filtertype.Equal {
	return &filtertype.Equal{Expected: fmt.Sprintf(format, a...)}
}

// Eq is an alias to Equal.
func Eq(expected any) *filtertype.Equal {
	return &filtertype.Equal{Expected: expected}
}
