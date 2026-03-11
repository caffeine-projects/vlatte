package filtertype

import (
	"fmt"
	"strconv"

	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// Falsy passes when the value is false.
type Falsy struct{}

// Match implements vlatte.Filter.
func (m *Falsy) Match(v any) (bool, error) {
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
		return Error(fmt.Errorf("falsy: error parsing value to bool"))
	}

	if b {
		return Mismatch("Falsy() Expected false but it is actually true")
	}

	return Success()
}

// Describe implements descriptor.SelfDescriptive.
func (m *Falsy) Describe() descriptor.Descriptor {
	return descriptor.KeyOnly("falsy")
}
