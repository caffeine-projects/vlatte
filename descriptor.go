package vlatte

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
)

var _ json.Marshaler = (*Descriptor)(nil)

var (
	// ErrDescriptorKeyEmpty is the panic of the constructors given an empty key, and the error of [Descriptor.Validate]
	// on a descriptor without one.
	ErrDescriptorKeyEmpty = errors.New("descriptor: key is empty")

	// ErrDescriptorSpecNotValidType is the error of [Descriptor.Validate] and [Descriptor.Marshallable] on a descriptor
	// whose Props is not []any, map[string]any, a [Descriptor] or nil, and of [Descriptor.Merge] on one whose Props is
	// not a map.
	ErrDescriptorSpecNotValidType = errors.New("descriptor: spec is not a valid type")

	// ErrDescriptorKeyAlreadyExists is the error of [Descriptor.Merge] when the map already holds the descriptor's key.
	ErrDescriptorKeyAlreadyExists = errors.New("descriptor: key already exists")
)

// SelfDescriptive is an interface that allows a type to describe itself.
type SelfDescriptive interface {
	Describe() Descriptor
}

// DescriptorPropsType is the constraint of the props [NewDescriptor] and [DescribeRoot] take: the types a [Descriptor]
// marshals.
type DescriptorPropsType interface {
	[]any | map[string]any | Descriptor
}

// DescriptorProps describes the properties of a [SelfDescriptive] describle type.
type DescriptorProps any

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
	Props DescriptorProps

	root bool
	err  error // why it can't be described, from DescribeFail
}

// NewDescriptor creates a descriptor with the given key and props. It panics with [ErrDescriptorKeyEmpty] if key is
// empty.
func NewDescriptor[T DescriptorPropsType](key string, props T) Descriptor {
	if len(key) == 0 {
		panic(ErrDescriptorKeyEmpty)
	}

	return Descriptor{Key: key, Props: props}
}

// DescribeSingleArg creates a descriptor with the given key and item as its only argument. It panics with
// [ErrDescriptorKeyEmpty] if key is empty.
func DescribeSingleArg(key string, item any) Descriptor {
	return NewDescriptor(key, []any{item})
}

// DescribeKeyOnly creates a descriptor with the given key and no arguments: it renders as a list of the key alone. It
// panics with [ErrDescriptorKeyEmpty] if key is empty.
func DescribeKeyOnly(key string) Descriptor {
	return NewDescriptor[[]any](key, nil)
}

// DescribeKeyOnlyMaybe creates a descriptor with the given key and, when item is [SelfDescriptive],
// its description as the only argument. Any other item is ignored, as in [DescribeKeyOnly].
func DescribeKeyOnlyMaybe(key string, item any) Descriptor {
	if self, ok := item.(SelfDescriptive); ok {
		return DescribeSingleArg(key, self.Describe())
	}

	return DescribeKeyOnly(key)
}

// DescribeMultiArgs creates a descriptor with the given key and the list of properties. It panics with
// [ErrDescriptorKeyEmpty] if key is empty.
func DescribeMultiArgs(key string, props ...any) Descriptor {
	if len(key) == 0 {
		panic(ErrDescriptorKeyEmpty)
	}

	return NewDescriptor(key, props)
}

// DescribeKeyValuePairs creates a descriptor with the given key and the list of key/value pairs. It panics with
// [ErrDescriptorKeyEmpty] if key is empty, and panics if props has an odd length or a key in props is not a string.
func DescribeKeyValuePairs(key string, props ...any) Descriptor {
	if len(key) == 0 {
		panic(ErrDescriptorKeyEmpty)
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

	return NewDescriptor(key, m)
}

// DescribeMaybeItems is a helper function to create a descriptor with items that might be also [SelfDescriptive]
// descriptive. In the case the child is not a [SelfDescriptive] descriptive type, it will be ignored. It panics with
// [ErrDescriptorKeyEmpty] if key is empty.
func DescribeMaybeItems(key string, items []any) Descriptor {
	if len(key) == 0 {
		panic(ErrDescriptorKeyEmpty)
	}

	props := make([]any, 0, len(items))
	for _, item := range items {
		if self, ok := item.(SelfDescriptive); ok {
			props = append(props, self.Describe())
		}
	}

	return NewDescriptor(key, props)
}

// DescribeFail creates a descriptor of something a description can't hold, such as a function. Encoding it, or any
// description that holds it, fails with err, rather than describe it as something else. It panics with
// [ErrDescriptorKeyEmpty] if key is empty.
func DescribeFail(key string, err error) Descriptor {
	if len(key) == 0 {
		panic(ErrDescriptorKeyEmpty)
	}

	return Descriptor{Key: key, err: err}
}

// DescribeRoot creates the top-level descriptor of a description, with the key "root" and the given props.
func DescribeRoot[T DescriptorPropsType](props T) Descriptor {
	return Descriptor{Key: "root", Props: props, root: true}
}

// Validate returns [ErrDescriptorKeyEmpty] if d has no key, the error [DescribeFail] gave it, or an error wrapping
// [ErrDescriptorSpecNotValidType] if its Props is not a type it marshals.
func (d Descriptor) Validate() error {
	if len(d.Key) == 0 {
		return ErrDescriptorKeyEmpty
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
		return fmt.Errorf("%w: expected: []any, map[string]any, Descriptor or nil. got: %T", ErrDescriptorSpecNotValidType, t)
	}

	return nil
}

// Err returns why d can't be described, as DescribeFail gave it, or nil.
func (d Descriptor) Err() error {
	return d.err
}

// Root reports whether d was created by [DescribeRoot].
func (d Descriptor) Root() bool {
	return d.root
}

// IsMap reports whether d's Props is a map[string]any: d renders without its key, for [Descriptor.Merge].
func (d Descriptor) IsMap() bool {
	_, ok := d.Props.(map[string]any)
	return ok
}

// IsArray reports whether d's Props is an []any. A nil Props, which also renders as an array, is not one.
func (d Descriptor) IsArray() bool {
	_, ok := d.Props.([]any)
	return ok
}

// Merge merges the descriptor into the given map.
// It will return an error if the key already exists in the map.
func (d Descriptor) Merge(m map[string]any) error {
	if _, ok := d.Props.(map[string]any); !ok {
		return fmt.Errorf("%w: expected: map[string]any. got: %T", ErrDescriptorSpecNotValidType, d.Props)
	}

	if _, ok := m[d.Key]; ok {
		return fmt.Errorf("%w: %s", ErrDescriptorKeyAlreadyExists, d.Key)
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

	return nil, fmt.Errorf("%w: expected: []any, map[string]any, Descriptor or nil. got: %T", ErrDescriptorSpecNotValidType, d.Props)
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

// IsSelfDescriptive returns the description of v and true if v is [SelfDescriptive], or a zero [Descriptor] and false.
func IsSelfDescriptive(v any) (Descriptor, bool) {
	if self, ok := v.(SelfDescriptive); ok {
		return self.Describe(), true
	}

	return Descriptor{}, false
}
