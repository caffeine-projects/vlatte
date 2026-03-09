package filter

import (
	"fmt"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type falsyMatcher struct {
}

func (m *falsyMatcher) Match(v any) (vlatte.Result, error) {
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
		return vlatte.Result{}, fmt.Errorf("falsy: error parsing value to bool")
	}

	if b {
		return vlatte.Result{Message: "Falsy() Expected false but it is actually true"}, nil
	}

	return vlatte.Result{Pass: true}, nil
}

func (m *falsyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("falsy")
}

// Falsy checks if the incoming request value is false.
func Falsy() vlatte.Filter {
	return &falsyMatcher{}
}
