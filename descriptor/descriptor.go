// Package descriptor provides a way to describe a type, allowing it to be properly marshalled.
package descriptor

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
)

var _ json.Marshaler = (*Descriptor)(nil)

var (
	ErrKeyEmpty         = errors.New("descriptor: key is empty")
	ErrSpecNotValidType = errors.New("descriptor: spec is not a valid type")
	ErrKeyAlreadyExists = errors.New("descriptor: key already exists")
)

// SelfDescriptive is an interface that allows a type to describe itself.
type SelfDescriptive interface {
	Describe() Descriptor
}

type P interface {
	[]any | map[string]any | Descriptor
}

func New[T P](key string, props T) Descriptor {
	if len(key) == 0 {
		panic(ErrKeyEmpty)
	}

	return Descriptor{Key: key, Props: props}
}

func SingleArg(key string, item any) Descriptor {
	return New(key, []any{item})
}

func KeyOnly(key string) Descriptor {
	return New[[]any](key, nil)
}

// KeyOnlyMaybe creates a descriptor with the given key and, when item is [SelfDescriptive],
// its description as the only argument. Any other item is ignored, as in [KeyOnly].
func KeyOnlyMaybe(key string, item any) Descriptor {
	if self, ok := item.(SelfDescriptive); ok {
		return SingleArg(key, self.Describe())
	}

	return KeyOnly(key)
}

// MultiArgs creates a descriptor with the given key and the list of properties.
func MultiArgs(key string, props ...any) Descriptor {
	if len(key) == 0 {
		panic(ErrKeyEmpty)
	}

	return New(key, props)
}

// WithKeyValuePairs creates a descriptor with the given key and the list of key/value pairs.
func WithKeyValuePairs(key string, props ...any) Descriptor {
	if len(key) == 0 {
		panic(ErrKeyEmpty)
	}

	if len(props)%2 != 0 {
		panic(fmt.Errorf("descriptor: expected an even number of key/value props. got: %d", len(props)))
	}

	m := make(map[string]any, len(props)/2)
	for i := 0; i < len(props); i += 2 {
		k, ok := props[i].(string)
		if !ok {
			panic(fmt.Errorf("descriptor: expected a string key at index %d. got: %T. value: %v", i, props[i], props[i]))
		}

		m[k] = props[i+1]
	}

	return New(key, m)
}

// WithMaybeItems is a helper function to create a descriptor with items that might be also [SelfDescriptive] descriptive.
// In the case the child is not a [SelfDescriptive] descriptive type, it will be ignored.
func WithMaybeItems(key string, items []any) Descriptor {
	if len(key) == 0 {
		panic(ErrKeyEmpty)
	}

	props := make([]any, 0, len(items))
	for _, item := range items {
		if self, ok := item.(SelfDescriptive); ok {
			props = append(props, self.Describe())
		}
	}

	return New(key, props)
}

// Fail creates a descriptor of something a description can't hold, such as a function. Encoding it, or any
// description that holds it, fails with err, rather than describe it as something else.
func Fail(key string, err error) Descriptor {
	if len(key) == 0 {
		panic(ErrKeyEmpty)
	}

	return Descriptor{Key: key, err: err}
}

func Root[T P](props T) Descriptor {
	return Descriptor{Key: "root", Props: props, root: true}
}

// Props describes the properties of a [SelfDescriptive] describle type.
type Props any

// Descriptor describes a type. Props decides how it marshals:
//
//	Descriptor{Key: "eq", Props: []any{"a", 1}}          renders ["eq","a",1]
//	Descriptor{Key: "eq", Props: nil}                    renders ["eq"]
//	Descriptor{Key: "eq", Props: map[string]any{"a": 1}} renders {"a":1}
//
// A map leaves Key out: [Descriptor.Merge] places it under Key in a parent map.
// A [Descriptor] as Props renders as that descriptor.
type Descriptor struct {
	Key   string
	Props Props

	root bool
	err  error // why it can't be described, from Fail
}

func (d Descriptor) Validate() error {
	if len(d.Key) == 0 {
		return ErrKeyEmpty
	}

	if d.err != nil {
		return d.err
	}

	switch t := d.Props.(type) {
	case nil:
	case Descriptor:
	case []any:
	case map[string]any:
		break
	default:
		return fmt.Errorf("%w: expected: []any, map[string]any, Descriptor or nil. got: %T", ErrSpecNotValidType, t)
	}

	return nil
}

// Err returns why d can't be described, as Fail gave it, or nil.
func (d Descriptor) Err() error {
	return d.err
}

func (d Descriptor) Root() bool {
	return d.root
}

func (d Descriptor) IsMap() bool {
	_, ok := d.Props.(map[string]any)
	return ok
}

func (d Descriptor) IsArray() bool {
	_, ok := d.Props.([]any)
	return ok
}

// Merge merges the descriptor into the given map.
// It will return an error if the key already exists in the map.
func (d Descriptor) Merge(m map[string]any) error {
	if _, ok := d.Props.(map[string]any); !ok {
		return fmt.Errorf("%w: expected: map[string]any. got: %T", ErrSpecNotValidType, d.Props)
	}

	if _, ok := m[d.Key]; ok {
		return fmt.Errorf("%w: %s", ErrKeyAlreadyExists, d.Key)
	}

	m[d.Key] = d.Props

	return nil
}

// String returns the string representation of the descriptor.
func (d Descriptor) String() string {
	return fmt.Sprintf("%s(%v)", d.Key, d.Props)
}

// Marshallable returns the Descriptor in a format that can be marshalled by any encoder.
func (d Descriptor) Marshallable() (any, error) {
	if d.err != nil {
		return nil, d.err
	}

	switch t := d.Props.(type) {
	case nil:
		return []any{d.Key}, nil
	case Descriptor:
		return t, nil
	case []any:
		return append([]any{d.Key}, t...), nil
	case map[string]any:
		return d.Props, nil
	}

	return nil, fmt.Errorf("%w: expected: []any, map[string]any, Descriptor or nil. got: %T", ErrSpecNotValidType, d.Props)
}

// MarshalJSON implements the [json.Marshaler] interface.
// It will marshal the [Descriptor] based on the [Descriptor.Marshallable] function return.
func (d Descriptor) MarshalJSON() ([]byte, error) {
	m, err := d.Marshallable()
	if err != nil {
		return nil, err
	}

	return json.Marshal(m, json.Deterministic(true))
}

func IsSelfDescriptive(v any) (Descriptor, bool) {
	if self, ok := v.(SelfDescriptive); ok {
		return self.Describe(), true
	}

	return Descriptor{}, false
}
