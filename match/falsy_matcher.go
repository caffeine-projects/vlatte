package match

import (
	"errors"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// falsyMatcher passes when the value is false.
type falsyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *falsyMatcher) Name() string { return "Falsy" }

// Match implements vlatte.Matcher.
func (m *falsyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()
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
		return failure(errors.New("falsy: error parsing value to bool"))
	}

	if b {
		return mismatch("Falsy() Expected false but it is actually true")
	}

	return success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *falsyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("falsy")
}
