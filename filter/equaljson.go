package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// EqualJSON matches JSON values.
func EqualJSON(expected any) *filtertype.EqualJSON {
	return &filtertype.EqualJSON{Expected: expected}
}

// EQJ is an alias for EqualJSON.
func EQJ(expected any) *filtertype.EqualJSON {
	return &filtertype.EqualJSON{Expected: expected}
}
