package vlatte

import (
	"math"
	"testing"
)

func TestArithmetic(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "integers stay integers", text: `{{ add 2 3 }} {{ sub 2 3 }} {{ mul 2 3 }} {{ div 7 2 }} {{ mod -7 2 }}`, want: "5 -1 6 3 -1"},
		{name: "a float makes a float", text: `{{ add 1 0.5 }} {{ div 7 2.0 }} {{ mod 7.5 2 }}`, want: "1.5 3.5 1.5"},
		{name: "a float difference", text: `{{ sub 2.5 1 }}`, want: "1.5"},
		{name: "unsigned integers", text: `{{ add . 1 }}`, data: uint8(7), want: "8"},
		{name: "an unsigned integer too large for an int", text: `{{ add . 1 }}`, data: uint64(math.MaxUint64), err: "error calling add: 18446744073709551615 overflows int"},
		{name: "JSON numbers are floats", text: `{{ index (fromJSON "[4]") 0 | printf "%T" }} {{ mul (index (fromJSON "[4]") 0) 2 }}`, want: "float64 8"},
		{name: "results feed other functions", text: `{{ substring 0 (sub (len .) 1) . }}`, data: "abc", want: "ab"},
		{
			// An overflow would answer a wrong number: it fails instead.
			name: "integer overflow", text: `{{ add 9223372036854775807 1 }}`, err: "error calling add: integer overflow",
		},
		{name: "integer overflow below", text: `{{ sub -9223372036854775807 2 }}`, err: "error calling sub: integer overflow"},
		{name: "integer overflow of a product", text: `{{ mul 4611686018427387904 2 }}`, err: "error calling mul: integer overflow"},
		{name: "integer overflow of a quotient", text: `{{ div -9223372036854775808 -1 }}`, err: "error calling div: integer overflow"},
		{name: "integer division by zero", text: `{{ div 1 0 }}`, err: "division by zero"},
		{name: "float division by zero", text: `{{ div 1.0 0 }}`, err: "division by zero"},
		{name: "mod by zero", text: `{{ mod 1 0 }}`, err: "division by zero"},
		{name: "float mod by zero", text: `{{ mod 1.5 0 }}`, err: "error calling mod: division by zero"},
		{name: "the second operand is checked too", text: `{{ add 1 "1" }}`, err: `error calling add: "1" is not a number`},
		{
			// A query parameter is text: a template converts it, so "1" + 1 is never "11" or 2 by accident.
			name: "strings are refused", text: `{{ add "1" 1 }}`, err: `"1" is not a number: convert it with toInt or toFloat`,
		},
	})
}

func TestNumberConversions(t *testing.T) {
	runFuncCases(t, []funcCase{
		{name: "toInt of a numeric string", text: `{{ add (toInt "41") 1 }}`, want: "42"},
		{name: "toInt of a float string without a fraction", text: `{{ toInt "3.0" }}`, want: "3"},
		{name: "toInt of a JSON number", text: `{{ toInt (index (fromJSON "[7]") 0) }}`, want: "7"},
		{name: "toInt of an int", text: `{{ toInt 7 }}`, want: "7"},
		{
			// A query parameter is exact: rounding it through a float64 would answer another number.
			name: "toInt of a string beyond float64 precision", text: `{{ toInt "9007199254740993.0" }}`, want: "9007199254740993",
		},
		{name: "toInt of a string below the range of int", text: `{{ toInt "-9223372036854775809" }}`, err: `error calling toInt: "-9223372036854775809" is not an int`},
		{name: "toInt of a string with a fraction", text: `{{ toInt "2.0000000000000001" }}`, err: `error calling toInt: "2.0000000000000001" is not an int`},
		{name: "toInt of a string in exponent form", text: `{{ toInt "1e3" }}`, err: `error calling toInt: "1e3" is not an int`},
		{name: "toInt of a fraction", text: `{{ toInt 2.5 }}`, err: "2.5 is not an int"},
		{name: "toInt of text", text: `{{ toInt "abc" }}`, err: `"abc" is not a number`},
		{name: "toInt of a bool", text: `{{ toInt true }}`, err: "true is not a number"},
		{name: "toFloat of a string", text: `{{ toFloat "2.5" }}`, want: "2.5"},
		{name: "toFloat of an int", text: `{{ div (toFloat 7) 2 }}`, want: "3.5"},
		{name: "toFloat of text", text: `{{ toFloat "x" }}`, err: `"x" is not a number`},
		{name: "toFloat of a bool", text: `{{ toFloat true }}`, err: "error calling toFloat: true is not a number"},
	})
}
