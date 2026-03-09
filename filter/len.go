package filter

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type lenMatcher struct {
	length int
}

func (m *lenMatcher) Match(v any) (vlatte.Result, error) {
	value := reflect.ValueOf(v)
	actual := value.Len()
	if actual == m.length {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{
		Message: strings.
			Join([]string{"Len(", strconv.Itoa(m.length), ") Got: ", strconv.Itoa(actual)}, "")}, nil
}

func (m *lenMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("len", m.length)
}

// Len passes when the expected value length is equal to the incoming request value.
func Len(length int) vlatte.Filter {
	return &lenMatcher{length: length}
}
