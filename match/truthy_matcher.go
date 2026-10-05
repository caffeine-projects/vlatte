package match

import (
	"errors"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

// truthyMatcher passes when the value is true.
type truthyMatcher struct{}

// Name implements vlatte.Matcher.
func (m *truthyMatcher) Name() string { return "Truthy" }

// Match implements vlatte.Matcher.
func (m *truthyMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
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
		return failure(errors.New("truthy: error parsing value to bool"))
	}

	if !b {
		return mismatch("Truthy() Expected true but it is actually false")
	}

	return success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *truthyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("truthy")
}
