package filtertype

import (
	"fmt"
	"strconv"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Truthy passes when the value is true.
type Truthy struct{}

// Match implements vlatte.Filter.
func (m *Truthy) Match(arg vlatte.FilterArg) (bool, error) {
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
		return Error(fmt.Errorf("truthy: error parsing value to bool"))
	}

	if !b {
		return Mismatch("Truthy() Expected true but it is actually false")
	}

	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *Truthy) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("truthy")
}
