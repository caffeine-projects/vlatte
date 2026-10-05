package anyconv

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
)

// Stringify returns a string representation of v for use in messages.
func Stringify(v any) string {
	switch s := v.(type) {
	case fmt.Stringer:
		return s.String()
	case []byte:
		return string(s)
	case string:
		return s
	case int:
		return strconv.Itoa(s)
	case int64:
		return strconv.FormatInt(s, 10)
	case int32:
		return strconv.FormatInt(int64(s), 10)
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(s), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(s)
	case *http.Request:
		return "*http.Request"
	case nil:
		return "<nil>"
	default:
		t := reflect.TypeOf(s)
		switch t.Kind() {
		case reflect.Pointer:
			return t.String()
		}
	}

	return fmt.Sprintf("%v", v)
}

func ToFloat64(v any) (float64, error) {
	switch e := v.(type) {
	case string:
		return strconv.ParseFloat(e, 64)
	case float64:
		return e, nil
	case float32:
		return float64(e), nil
	case int:
		return float64(e), nil
	case int32:
		return float64(e), nil
	case int64:
		return float64(e), nil
	default:
		return 0, errors.New("value cannot be parsed to float64")
	}
}
