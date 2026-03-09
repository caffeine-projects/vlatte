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

func (m *lenMatcher) Match(v any) (bool, vlatte.Reason, error) {
	value := reflect.ValueOf(v)
	actual := value.Len()
	if actual == m.length {
		return success()
	}

	return mismatch(strings.Join(
		[]string{"Len(", strconv.Itoa(m.length), ") Got: ", strconv.Itoa(actual)},
		"",
	))
}

func (m *lenMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("len", m.length)
}

// Len passes when the expected value length is equal to the incoming request value.
func Len(length int) vlatte.Filter {
	return &lenMatcher{length: length}
}
