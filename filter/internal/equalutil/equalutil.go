package equalutil

import (
	"reflect"
	"strconv"
)

// EqualValues reports whether expected and actual are equal, optionally with lenient type conversion.
func EqualValues(expected, actual any, lenient bool) bool {
	// Fast paths for the most common scalar types, avoiding reflect.DeepEqual overhead.
	switch e := expected.(type) {
	case string:
		if a, ok := actual.(string); ok {
			return e == a
		}
	case int:
		if a, ok := actual.(int); ok {
			return e == a
		}
	case int64:
		if a, ok := actual.(int64); ok {
			return e == a
		}
	case float64:
		if a, ok := actual.(float64); ok {
			return e == a
		}
	case bool:
		if a, ok := actual.(bool); ok {
			return e == a
		}
	}

	if reflect.DeepEqual(expected, actual) {
		return true
	}

	actualType := reflect.TypeOf(actual)
	if actualType == nil {
		return false
	}

	expectedValue := reflect.ValueOf(expected)
	if expectedValue.IsValid() && expectedValue.Type().ConvertibleTo(actualType) {
		if reflect.DeepEqual(expectedValue.Convert(actualType).Interface(), actual) {
			return true
		}
	}

	eType := reflect.TypeOf(expected)
	if eType == nil {
		return false
	}

	actualKind := actualType.Kind()
	expectedKind := eType.Kind()
	actualValue := reflect.ValueOf(actual)

	if actualKind != expectedKind && lenient {
		pass := laxEq(actualKind, expectedKind, actualValue, expectedValue, lenient)
		if pass {
			return true
		}

		return laxEq(expectedKind, actualKind, expectedValue, actualValue, lenient)
	}

	switch actualKind {
	case reflect.Array, reflect.Slice:
		aLen := actualValue.Len()
		bLen := expectedValue.Len()

		if aLen != bLen {
			return false
		}

		for i := 0; i < aLen; i++ {
			a := actualValue.Index(i).Interface()
			b := expectedValue.Index(i).Interface()

			if !EqualValues(b, a, lenient) {
				return false
			}
		}

		return true

	case reflect.Map:
		actualKeys := actualValue.MapKeys()
		expectedKeys := expectedValue.MapKeys()

		if len(expectedKeys) != len(actualKeys) {
			return false
		}

		expectedKeyType := expectedValue.Type().Key()
		actualKeyType := actualValue.Type().Key()

		if !expectedKeyType.ConvertibleTo(actualKeyType) {
			return false
		}

		for _, expectedKey := range expectedKeys {
			actualKey := expectedKey.Convert(actualKeyType)
			expectedEntry := expectedValue.MapIndex(expectedKey)
			actualEntry := actualValue.MapIndex(actualKey)

			if !actualEntry.IsValid() {
				return false
			}

			if !EqualValues(expectedEntry.Interface(), actualEntry.Interface(), lenient) {
				return false
			}
		}

		return true
	}

	return false
}

func laxEq(aKind, bKind reflect.Kind, aValue, bValue reflect.Value, lenient bool) bool {
	switch aKind {
	case reflect.String:
		switch bKind {
		case reflect.Float64, reflect.Float32:
			a, err := strconv.ParseFloat(aValue.String(), 64)
			if err != nil {
				return false
			}

			return EqualValues(a, bValue.Float(), lenient)

		case reflect.Bool:
			a, err := strconv.ParseBool(aValue.String())
			if err != nil {
				return false
			}

			return EqualValues(a, bValue.Bool(), lenient)

		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			a, err := strconv.ParseInt(aValue.String(), 10, 64)
			if err != nil {
				return false
			}

			return EqualValues(a, bValue.Int(), lenient)

		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			a, err := strconv.ParseUint(aValue.String(), 10, 64)
			if err != nil {
				return false
			}

			return EqualValues(a, bValue.Uint(), lenient)
		}
	}

	return false
}
