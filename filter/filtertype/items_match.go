package filtertype

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter/internal/equalutil"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

// ItemsMatch passes when the value slice has the same elements as expected (order-independent).
type ItemsMatch struct {
	Expected any
}

// Match implements vlatte.Filter.
func (m *ItemsMatch) Match(v any) (bool, vlatte.Reason, error) {
	a := reflect.ValueOf(v)
	b := reflect.ValueOf(m.Expected)

	aLen := a.Len()
	bLen := b.Len()

	if aLen != bLen {
		return Mismatch(strings.Join(
			[]string{"ItemsMatch() Expected value len to be ", strconv.Itoa(aLen), ". Got: ", strconv.Itoa(bLen)},
			"",
		))
	}

	var extraA, extraB []interface{}

	visited := make([]bool, bLen)

	for i := 0; i < aLen; i++ {
		element := a.Index(i).Interface()
		found := false

		for j := 0; j < bLen; j++ {
			if visited[j] {
				continue
			}

			if equalutil.EqualValues(b.Index(j).Interface(), element, false) {
				visited[j] = true
				found = true
				break
			}
		}

		if !found {
			extraA = append(extraA, element)
		}
	}

	for j := 0; j < bLen; j++ {
		if visited[j] {
			continue
		}

		extraB = append(extraB, b.Index(j).Interface())
	}

	if len(extraA) == 0 && len(extraB) == 0 {
		return Success()
	}

	return Mismatch("ItemsMatch() Items doesn't match")
}

// Describe implements descriptor.SelfDescriptive.
func (m *ItemsMatch) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("itemsMatch", m.Expected)
}
