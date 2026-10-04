package configutil

import (
	"errors"
	"fmt"
)

var (
	ErrNotPresent   = errors.New("key not present")
	ErrTypeMismatch = errors.New("type mismatch for value")
)

type ConfigUtil struct {
	m map[string]any
}

func New(m map[string]any) *ConfigUtil {
	return &ConfigUtil{m: m}
}

func (r *ConfigUtil) Str(k, d string) string {
	if v, ok := r.m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}

	return d
}

func (r *ConfigUtil) StrRequired(k string) (string, error) {
	if v, ok := r.m[k]; ok {
		s, ok := v.(string)
		if !ok {
			return "", errorTypeMismatch(k, v, "string")
		}

		return s, nil
	}

	return "", errorNotPresent(k)
}

func (r *ConfigUtil) Int(k string, d int) int {
	if v, ok := r.m[k]; ok {
		if i, ok := v.(int); ok {
			return i
		}
	}

	return d
}

func (r *ConfigUtil) Bool(k string, d bool) bool {
	if v, ok := r.m[k]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}

	return d
}

func (r *ConfigUtil) StrMap(k string) map[string]string {
	if v, ok := r.m[k]; ok {
		if m, ok := v.(map[string]string); ok {
			return m
		}
	}

	return nil
}

func (r *ConfigUtil) Has(k string) bool {
	_, ok := r.m[k]
	return ok
}

// Validators
// --

func (r *ConfigUtil) EitherRequired(k1, k2 string) error {
	if !r.Has(k1) && !r.Has(k2) {
		return fmt.Errorf("either key '%s' or '%s' must be present", k1, k2)
	}

	if r.Has(k1) && r.Has(k2) {
		return fmt.Errorf("only one of the keys '%s' or '%s' should be present", k1, k2)
	}

	return nil
}

func (r *ConfigUtil) Either(k1, k2 string) error {
	if r.Has(k1) && r.Has(k2) {
		return fmt.Errorf("only one of the keys '%s' or '%s' should be present", k1, k2)
	}

	return nil
}

func (r *ConfigUtil) RequiredOneOf(ks ...string) error {
	if len(ks) == 0 {
		return errors.New("no keys provided for validation")
	}

	found := false
	for _, k := range ks {
		if r.Has(k) {
			if found {
				return fmt.Errorf("only one of the keys %v should be present", ks)
			}
			found = true
		}
	}

	if !found {
		return fmt.Errorf("at least one of the keys %v must be present", ks)
	}

	return nil
}

func (r *ConfigUtil) OneOf(ks ...string) error {
	if len(ks) == 0 {
		return errors.New("no keys provided for validation")
	}

	found := false
	for _, k := range ks {
		if r.Has(k) {
			if found {
				return fmt.Errorf("only one of the keys %v should be present", ks)
			}
			found = true
		}
	}

	return nil
}

// Error Utilities
// --

func errorNotPresent(key string) error {
	return fmt.Errorf("%w: key=%s", ErrNotPresent, key)
}

func errorTypeMismatch(key string, value any, expectedType string) error {
	return fmt.Errorf("%w: key=%s, expected type=%s, got=%T", ErrTypeMismatch, key, expectedType, value)
}
