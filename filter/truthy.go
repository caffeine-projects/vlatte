package filter

import (
	"fmt"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type truthyMatcher struct {
}

func (m *truthyMatcher) Match(v any) (vlatte.Result, error) {
	var b bool
	var err error

	switch e := v.(type) {
	case bool:
		b = e
	case string:
		b, err = strconv.ParseBool(e)
	case int:
		b, err = strconv.ParseBool(strconv.FormatInt(int64(e), 10))
	}

	if err != nil {
		return vlatte.Result{}, fmt.Errorf("truthy: error parsing value to bool")
	}

	if !b {
		return vlatte.Result{Message: "Truthy() Expected true but it is actually false"}, nil
	}

	return vlatte.Result{Pass: true}, nil
}

func (m *truthyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("truthy")
}

// Truthy passes if the request value is true.
func Truthy() vlatte.Filter {
	return &truthyMatcher{}
}
