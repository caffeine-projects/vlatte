package filter

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/filtertype"
)

func Delegate(name string, predicate func(arg vlatte.FilterArg) (bool, error)) vlatte.Filter {
	return filtertype.NewDelegate(name, predicate)
}
