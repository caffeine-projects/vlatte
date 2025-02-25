// Package desc provides a way to describe a type, allowing it to be properly marshalled.
package desc

import (
	"encoding/json"
	"errors"
	"fmt"
)

var _ json.Marshaler = (*Descriptor)(nil)

var ErrKeyEmpty = errors.New("descriptor: key is empty")
var ErrSpecNotValidType = errors.New("descriptor: spec is not a valid type")
var ErrKeyAlreadyExists = errors.New("descriptor: key already exists")

// SelfDescribing is an interface that allows a type to describe itself.
type SelfDescribing interface {
	Describe() Descriptor
}

// Descriptor describes a type.
// The Key field is the name of the type and also will be used as the key in when Props is a map[string]any.
// Example:
//
//	Descriptor{Key: "eq", Props: map[string]any{"field1": "value1","field2": "value2"}}
//	will render the JSON: {"eq": {"field1": "value1","field2": "value2"}}
//	Descriptor{Key: "eq", Props: []any{"field1", "value1","field2", "value2"}}
//	will render the JSON: ["eq", "field1", "value1","field2", "value2"]
type Descriptor struct {
	Key   string
	Props any
}

func (d *Descriptor) Validate() error {
	if len(d.Key) == 0 {
		return ErrKeyEmpty
	}

	switch t := d.Props.(type) {
	case []any:
		break
	case map[string]any:
		break
	default:
		return fmt.Errorf("%w: expected: []any or map[string]any. got: %T", ErrSpecNotValidType, t)
	}

	return nil
}

func (d *Descriptor) Append(m map[string]any) error {
	if _, ok := d.Props.(map[string]any); !ok {
		return fmt.Errorf("%w: expected: map[string]any. got: %T", ErrSpecNotValidType, d.Props)
	}

	if _, ok := m[d.Key]; ok {
		return fmt.Errorf("%w: %s", ErrKeyAlreadyExists, d.Key)
	}

	m[d.Key] = d.Props

	return nil
}

func (d *Descriptor) MarshalJSON() ([]byte, error) {
	switch t := d.Props.(type) {
	case []any:
		return json.Marshal(append([]any{d.Key}, t...))
	case map[string]any:
		return json.Marshal(d.Props)
	}

	return nil, fmt.Errorf("%w: expected: []any or map[string]any. got: %T", ErrSpecNotValidType, d.Props)
}
