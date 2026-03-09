package filter

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/descriptor"
)

type itemMatcher struct {
	index   int
	matcher vlatte.Filter
}

func (m *itemMatcher) Match(v any) (vlatte.Result, error) {
	if m.index < 0 {
		return vlatte.Result{}, fmt.Errorf("item: index must greater or equal to 0. got %d", m.index)
	}

	typeOfV := reflect.TypeOf(v)
	kind := typeOfV.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return vlatte.Result{}, fmt.Errorf("item: %d: it only works with slices. got: %T", m.index, v)
	}

	vv := reflect.ValueOf(v)
	if vv.Len() == 0 {
		return vlatte.Result{Message: strings.Join([]string{"Item(", strconv.Itoa(m.index), ") Empty array"}, "")}, nil
	}

	res, err := m.matcher.Match(vv.Index(m.index).Interface())
	if err != nil {
		return vlatte.Result{}, fmt.Errorf("item: %d: %w", m.index, err)
	}

	if res.Pass {
		return vlatte.Result{Pass: true}, nil
	}

	return vlatte.Result{
		Message: strings.Join([]string{"Item(", strconv.Itoa(m.index), ") ", res.Message}, ""),
	}, nil
}

func (m *itemMatcher) Describe() descriptor.Descriptor {
	return descriptor.MultiArgs("item", m.index, describe(m.matcher))
}

// Item matches a specific array item from the incoming request value.
func Item(index int, matcher vlatte.Filter) vlatte.Filter {
	return &itemMatcher{index: index, matcher: matcher}
}
