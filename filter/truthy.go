package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Truthy checks if the incoming request value is true.
func Truthy() *filtertype.Truthy {
	return &filtertype.Truthy{}
}
