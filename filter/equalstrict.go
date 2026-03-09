package filter

import (
	"fmt"

	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// StrictEqual strictly compares the expected value with incoming request values.
func StrictEqual(expected any) *filtertype.EqualStrict {
	return &filtertype.EqualStrict{Expected: expected}
}

// StrictEqualf is short-hand to format the expected value.
func StrictEqualf(format string, a ...any) *filtertype.EqualStrict {
	return &filtertype.EqualStrict{Expected: fmt.Sprintf(format, a...)}
}

// EQS is an alias for StrictEqual.
func EQS(expected any) *filtertype.EqualStrict {
	return &filtertype.EqualStrict{Expected: expected}
}
