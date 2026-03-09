package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// Nil matches when the value is nil.
func Nil() *filtertype.Nil {
	return &filtertype.Nil{}
}
