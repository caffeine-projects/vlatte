package filter

import (
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

// IsNil matches when the value is nil.
func IsNil() *filtertype.Nil {
	return &filtertype.Nil{}
}
