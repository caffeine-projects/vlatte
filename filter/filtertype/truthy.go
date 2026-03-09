package filtertype

import (
	"fmt"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// TruthyMatcher passes when the value is true.
type TruthyMatcher struct{}

// Match implements vlatte.Filter.
func (m *TruthyMatcher) Match(v any) (bool, vlatte.Reason, error) {
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
		return Error(fmt.Errorf("truthy: error parsing value to bool"))
	}

	if !b {
		return Mismatch("Truthy() Expected true but it is actually false")
	}

	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *TruthyMatcher) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("truthy")
}
