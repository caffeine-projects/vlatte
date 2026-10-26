package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// EqualJSON matches a value whose JSON equals expected, by StrictEqual's rule. expected is JSON text,
// or a value of any type, written as JSON. A value that is text, such as a request body, is read as JSON;
// any other value is written as JSON. It panics when expected isn't JSON, or can't be written as JSON.
//
// By default the JSON must be equal in full: lists in the same order, and objects with the same members. Options
// loosen it: [IgnoreArrayOrder] and [IgnoreExtraElements].
func EqualJSON(expected any, opts ...EqualJSONOption) vlatte.Matcher {
	m := &equalJSONMatcher{Expected: expected, want: mustJSON(expected)}
	for _, opt := range opts {
		opt.applyJSON(&m.opts)
	}

	return m
}

// EQJ is an alias for EqualJSON.
func EQJ(expected any, opts ...EqualJSONOption) vlatte.Matcher {
	return EqualJSON(expected, opts...)
}

// EqualJSONOption configures [EqualJSON].
type EqualJSONOption interface {
	applyJSON(o *jsonOptions)
}

// jsonOptions are what the options of EqualJSON loosen.
type jsonOptions struct {
	ignoreArrayOrder    bool
	ignoreExtraElements bool
}

type ignoreArrayOrder struct{}

func (ignoreArrayOrder) applyJSON(o *jsonOptions) { o.ignoreArrayOrder = true }

// IgnoreArrayOrder makes [EqualJSON] match lists whose items pair up in any order: each expected item with an item
// of its own.
func IgnoreArrayOrder() EqualJSONOption { return ignoreArrayOrder{} }

type ignoreExtraElements struct{}

func (ignoreExtraElements) applyJSON(o *jsonOptions) { o.ignoreExtraElements = true }

// IgnoreExtraElements makes [EqualJSON] ignore what the value has beyond what is expected: object members it doesn't
// expect, and list items. The expected items of a list must still be there, in order, unless [IgnoreArrayOrder] is
// given too: {"a": [1, 3]} matches {"a": [1, 2, 3], "b": 4}, but not {"a": [3, 1]}.
func IgnoreExtraElements() EqualJSONOption { return ignoreExtraElements{} }

// mustJSON returns what expected means as JSON: JSON text, read strictly, or a value written as JSON.
func mustJSON(expected any) any {
	var (
		want any
		err  error
	)

	if text, ok := value.Text(expected); ok {
		want, err = value.DecodeJSON([]byte(text))
	} else {
		want, err = value.ViaJSON(expected)
	}

	if err != nil {
		panic(fmt.Errorf("match: eqj: %w", err))
	}

	return want
}
