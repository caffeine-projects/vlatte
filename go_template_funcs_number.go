package vlatte

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

var (
	// errDivByZero says div or mod got 0 as divisor.
	errDivByZero = errors.New("division by zero")

	// errOverflow says the result of integer arithmetic doesn't fit an int.
	errOverflow = errors.New("integer overflow")
)

// number is a numeric argument of arithmetic: an integer, unless isFloat.
type number struct {
	i       int
	f       float64
	isFloat bool
}

// toNumber reads a number from v, of any integer or float type. Strings are not numbers: toInt and toFloat convert
// them, so a template says which it means.
func toNumber(v any) (number, error) {
	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return number{i: int(rv.Int())}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if rv.Uint() > math.MaxInt {
			return number{}, fmt.Errorf("%d overflows int", rv.Uint())
		}

		return number{i: int(rv.Uint())}, nil
	case reflect.Float32, reflect.Float64:
		return number{f: rv.Float(), isFloat: true}, nil
	default:
		return number{}, fmt.Errorf("%#v is not a number: convert it with toInt or toFloat", v)
	}
}

func (n number) float() float64 {
	if n.isFloat {
		return n.f
	}

	return float64(n.i)
}

// arithmetic applies an operation to a and b: on ints when both are integers, and on float64s otherwise.
func arithmetic(a, b any, ints func(x, y int) (int, error), floats func(x, y float64) (float64, error)) (any, error) {
	x, err := toNumber(a)
	if err != nil {
		return nil, err
	}

	y, err := toNumber(b)
	if err != nil {
		return nil, err
	}

	var r any
	if !x.isFloat && !y.isFloat {
		r, err = ints(x.i, y.i)
	} else {
		r, err = floats(x.float(), y.float())
	}

	if err != nil {
		return nil, err
	}

	return r, nil
}

func add(a, b any) (any, error) {
	return arithmetic(a, b,
		func(x, y int) (int, error) {
			if r := x + y; (r > x) == (y > 0) {
				return r, nil
			}

			return 0, errOverflow
		},
		func(x, y float64) (float64, error) { return x + y, nil })
}

func sub(a, b any) (any, error) {
	return arithmetic(a, b,
		func(x, y int) (int, error) {
			if r := x - y; (r < x) == (y > 0) {
				return r, nil
			}

			return 0, errOverflow
		},
		func(x, y float64) (float64, error) { return x - y, nil })
}

func mul(a, b any) (any, error) {
	return arithmetic(a, b,
		func(x, y int) (int, error) {
			r := x * y
			if x != 0 && (r/x != y || (x == -1 && y == math.MinInt)) {
				return 0, errOverflow
			}

			return r, nil
		},
		func(x, y float64) (float64, error) { return x * y, nil })
}

// div divides a by b. Integers divide as Go's do, truncating.
func div(a, b any) (any, error) {
	return arithmetic(a, b,
		func(x, y int) (int, error) {
			if y == 0 {
				return 0, errDivByZero
			}

			return x / y, nil
		},
		func(x, y float64) (float64, error) {
			if y == 0 {
				return 0, errDivByZero
			}

			return x / y, nil
		})
}

// mod returns the remainder of a divided by b, with the sign of a, as Go's % and math.Mod do.
func mod(a, b any) (any, error) {
	return arithmetic(a, b,
		func(x, y int) (int, error) {
			if y == 0 {
				return 0, errDivByZero
			}

			return x % y, nil
		},
		func(x, y float64) (float64, error) {
			if y == 0 {
				return 0, errDivByZero
			}

			return math.Mod(x, y), nil
		})
}

// toInt converts v to an int: an integer, a float without a fraction, or a string of one, such as a query parameter.
func toInt(v any) (int, error) {
	if s, ok := v.(string); ok {
		if i, err := strconv.Atoi(s); err == nil {
			return i, nil
		}

		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", s)
		}

		v = f
	}

	n, err := toNumber(v)
	if err != nil {
		return 0, err
	}

	if !n.isFloat {
		return n.i, nil
	}

	if n.f != math.Trunc(n.f) || n.f < math.MinInt || n.f >= math.MaxInt {
		return 0, fmt.Errorf("%v is not an int", n.f)
	}

	return int(n.f), nil
}

// toFloat converts v to a float64: a number, or a string of one.
func toFloat(v any) (float64, error) {
	if s, ok := v.(string); ok {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", s)
		}

		return f, nil
	}

	n, err := toNumber(v)
	if err != nil {
		return 0, err
	}

	return n.float(), nil
}

// sameValue reports whether a and b are equal: numbers by value, whatever their types, integers exactly, and other values as
// reflect.DeepEqual has them.
func sameValue(a, b any) bool {
	x, errX := toNumber(a)
	y, errY := toNumber(b)

	switch {
	case errX != nil || errY != nil:
		return reflect.DeepEqual(a, b)
	case !x.isFloat && !y.isFloat:
		return x.i == y.i
	default:
		return x.float() == y.float()
	}
}
