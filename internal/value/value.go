// Package value is the data model that documents, settings and matchers share: how JSON is read,
// how text reads as a bool or a number, and how values compare.
//
// Values fall into classes: nil, bools, text (strings and byte slices), numbers (any Go int, uint or float),
// lists (other slices and arrays), objects (maps with string keys) and other values, such as structs.
// Values of one class compare by value. Across classes, only two readings happen, the ones text from a request,
// an environment variable or a flag needs: text compared with a number reads as a number, with ParseNumber,
// and anything compared with a bool reads as a bool, with Bool.
package value

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// Bool reads v as a bool the way strconv.ParseBool reads text: text as written, with no trimming;
// the numbers 1 and 0, as "1" and "0"; and a bool as itself. Nil, lists, objects and other values aren't bools.
func Bool(v any) (b, ok bool) {
	return boolOf(classify(v))
}

func boolOf(k kept) (b, ok bool) {
	switch k.class {
	case classBool:
		return k.b, true
	case classText:
		b, err := strconv.ParseBool(k.s)
		return b, err == nil
	case classNumber:
		if c, ok := compareNumbers(k.n, int64(1)); ok && c == 0 {
			return true, true
		}

		if c, ok := compareNumbers(k.n, int64(0)); ok && c == 0 {
			return false, true
		}
	}

	return false, false
}

// ParseNumber reads text written in the JSON number grammar, -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?,
// the way documents read numbers: an integer that fits an int64 keeps every digit, however it is written, so
// 9007199254740993, 9007199254740993.0 and 9.007199254740993e15 are one number; any other number is the nearest
// float64, kept as an int64 when that is whole.
// Other text, such as +1, 01, .5, 1_000, Inf or text with spaces, isn't a number, and neither are numbers
// beyond float64, such as 1e400.
func ParseNumber(s string) (any, bool) {
	if !isNumber(s) {
		return nil, false
	}

	n, err := number(s, false)

	return n, err == nil
}

// ParseInteger reads s, a decimal number, when its value is a whole number that fits an int64, with every digit:
// read as a float64 first, 9.007199254740993e15 would be 9007199254740992. It takes the JSON number grammar, and
// what YAML floats add to it: a leading + or zeros, and a point with no digits on one side, as in .5e1 or 5.
// ok is false for other text, and for numbers that aren't whole or don't fit.
func ParseInteger(s string) (int64, bool) {
	sign := ""
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		sign, s = "-", rest
	} else {
		s = strings.TrimPrefix(s, "+")
	}

	mantissa, exponent := s, "0"
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exponent = s[:i], s[i+1:]
	}

	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := whole + fraction

	e, err := strconv.Atoi(exponent)
	if digits == "" || strings.Trim(digits, "0123456789") != "" || (err != nil && !errors.Is(err, strconv.ErrRange)) {
		return 0, false
	}

	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, true
	}

	// The value is digits × 10^(e - len(fraction)). Far enough from 0, e makes it too large to fit, or leaves
	// a fraction; this bound also keeps the shift below from overflowing.
	if err != nil || e > 19+len(fraction) || e < -len(digits) {
		return 0, false
	}

	trimmed := strings.TrimRight(digits, "0")

	shift := e - len(fraction) + len(digits) - len(trimmed)
	if shift < 0 || len(trimmed)+shift > 19 {
		return 0, false
	}

	n, err := strconv.ParseInt(sign+trimmed+strings.Repeat("0", shift), 10, 64)

	return n, err == nil
}

// isNumber reports whether s is written in the JSON number grammar.
func isNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}

	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && isDigit(s[i]):
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	default:
		return false
	}

	if i < len(s) && s[i] == '.' {
		if i++; i == len(s) || !isDigit(s[i]) {
			return false
		}

		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}

	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		if i++; i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}

		if i == len(s) || !isDigit(s[i]) {
			return false
		}

		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}

	return i == len(s)
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

// number reads text the JSON grammar accepts, as ParseNumber does. A number beyond float64 is an error,
// or ±Inf when lenient.
func number(text string, lenient bool) (any, error) {
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n, nil
	}

	if n, ok := ParseInteger(text); ok {
		return n, nil
	}

	// Text the grammar accepts fails to parse only when it is beyond float64; ParseFloat then gives ±Inf.
	f, err := strconv.ParseFloat(text, 64)
	if err != nil && !lenient {
		return nil, fmt.Errorf("number %s is out of range", text)
	}

	return whole(f), nil
}

// whole returns f as an int64 when its value is whole and fits, so a number means the same however it is written.
func whole(f float64) any {
	if f == math.Trunc(f) && f >= -(1<<63) && f < 1<<63 {
		return int64(f)
	}

	return f
}

// Number returns v, a Go number of any int, uint or float kind, named types included, with its exact value:
// as an int64, as a uint64 when above math.MaxInt64, or as a float64. Other values aren't numbers.
func Number(v any) (any, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return n, true
	case int:
		return int64(n), true
	}

	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := rv.Uint()
		if u > math.MaxInt64 {
			return u, true
		}

		return int64(u), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return nil, false
	}
}

// Text returns v as text: a string, a byte slice, or a value of a named type of either, such as json.Number.
func Text(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []byte:
		return string(t), true
	}

	rv := reflect.ValueOf(v)

	switch {
	case rv.Kind() == reflect.String:
		return rv.String(), true
	case isBytes(rv):
		return string(rv.Bytes()), true
	default:
		return "", false
	}
}

// List returns the items of v, a slice or an array other than a byte slice, which is text.
func List(v any) ([]any, bool) {
	switch l := v.(type) {
	case []any:
		return l, true
	case []string:
		items := make([]any, len(l))
		for i, s := range l {
			items[i] = s
		}

		return items, true
	}

	rv := reflect.ValueOf(v)
	if (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) || isBytes(rv) {
		return nil, false
	}

	items := make([]any, rv.Len())
	for i := range rv.Len() {
		items[i] = rv.Index(i).Interface()
	}

	return items, true
}

// Object returns the members of v, a map whose keys are strings, or of a named string type.
func Object(v any) (map[string]any, bool) {
	if o, ok := v.(map[string]any); ok {
		return o, true
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}

	members := make(map[string]any, rv.Len())
	for iter := rv.MapRange(); iter.Next(); {
		members[iter.Key().String()] = iter.Value().Interface()
	}

	return members, true
}

func isBytes(rv reflect.Value) bool {
	return rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8
}

// class is a class of values, which decides how values compare.
type class uint8

const (
	classNil class = iota
	classBool
	classText
	classNumber
	classList
	classObject
	classOther
)

// kept is a value with its class, in the form values of its class compare.
type kept struct {
	class class
	b     bool
	s     string
	n     any // a number as Number gives it
	list  []any
	obj   map[string]any
	other any
}

func classify(v any) kept {
	if v == nil {
		return kept{class: classNil}
	}

	if b, ok := v.(bool); ok {
		return kept{class: classBool, b: b}
	}

	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Bool {
		return kept{class: classBool, b: rv.Bool()}
	}

	if s, ok := Text(v); ok {
		return kept{class: classText, s: s}
	}

	if n, ok := Number(v); ok {
		return kept{class: classNumber, n: n}
	}

	if l, ok := List(v); ok {
		return kept{class: classList, list: l}
	}

	if o, ok := Object(v); ok {
		return kept{class: classObject, obj: o}
	}

	return kept{class: classOther, other: v}
}
