package value

import (
	"math"
	"math/big"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// Named types stand for the named types Go code passes, such as json.Number, a named []byte or an enum.
type (
	named     string
	namedByte []byte
	namedInt  int16
	namedBool bool
)

type point struct {
	X int
	Y string
}

func TestParseNumber(t *testing.T) {
	// Text reads as a number only in the JSON grammar, the way documents write numbers,
	// and means what the same number in a document means.
	valid := []struct {
		text string
		want any
	}{
		{"0", int64(0)},
		{"-0", int64(0)},
		{"5", int64(5)},
		{"-5", int64(-5)},
		{"5.0", int64(5)},
		{"5e0", int64(5)},
		{"1E2", int64(100)},
		{"1e-1", 0.1},
		{"1.5", 1.5},
		{"0.1", 0.1},
		{"-0.0", int64(0)},
		{"9007199254740993", int64(9007199254740993)},
		{"9223372036854775807", int64(math.MaxInt64)},
		{"-9223372036854775808", int64(math.MinInt64)},
		{"9223372036854775808", float64(1 << 63)},
		{"1e308", 1e308},
		// An integer keeps every digit however it is written: read as a float64 first, these were 9007199254740992.
		{"9.007199254740993e15", int64(9007199254740993)},
		{"9007199254740993.0", int64(9007199254740993)},
		{"90071992547409930e-1", int64(9007199254740993)},
		{"9.223372036854775807e18", int64(math.MaxInt64)},
		{"-9.223372036854775808e18", int64(math.MinInt64)},
		{"9.223372036854775808e18", float64(1 << 63)},
		{"1e0000000000000000000005", int64(100000)},
		{"0e99999999999999999999", int64(0)},
		{"1e-99999999999999999999", int64(0)},
	}

	for _, tc := range valid {
		got, ok := ParseNumber(tc.text)
		require.True(t, ok, tc.text)
		require.Equal(t, tc.want, got, tc.text)
	}

	for _, text := range []string{
		"", "-", "+1", "01", "00", "-01", ".5", "5.", "1e", "1e+", "1.e1", " 1", "1 ", "0x10", "1_000",
		"Inf", "-Inf", "NaN", "1e400", "-1e400", "1.5.5", "--1", "1-", "١", "５",
	} {
		_, ok := ParseNumber(text)
		require.False(t, ok, "%q isn't a JSON number", text)
	}
}

func TestParseInteger(t *testing.T) {
	// Whole numbers keep every digit, in the JSON grammar and in what YAML floats add to it.
	valid := []struct {
		text string
		want int64
	}{
		{"5", 5},
		{"-5", -5},
		{"+5", 5},
		{"007", 7},
		{"5.", 5},
		{".5e1", 5},
		{"-0.0", 0},
		{"0e99999999999999999999", 0},
		{"1e0000000000000000000005", 100000},
		{"9.007199254740993e15", 9007199254740993},
		{"92233720368547758070e-1", math.MaxInt64},
		{"-9.223372036854775808E+18", math.MinInt64},
	}

	for _, tc := range valid {
		got, ok := ParseInteger(tc.text)
		require.True(t, ok, tc.text)
		require.Equal(t, tc.want, got, tc.text)
	}

	for _, text := range []string{
		"", "-", "+", ".", "e5", "1e", "1e+", "+-5", "--5", "1.2.3", "1_000", "0x10", "١", " 5",
		"1.5", "1e-1", "1e19", "9223372036854775808", "-9223372036854775809", "1e99999999999999999999",
		"1e-99999999999999999999",
	} {
		_, ok := ParseInteger(text)
		require.False(t, ok, "%q", text)
	}
}

func TestBool(t *testing.T) {
	// Every bool, in any source, reads the way strconv.ParseBool reads text.
	tests := []struct {
		value any
		want  bool
		ok    bool
	}{
		{true, true, true},
		{false, false, true},
		{namedBool(true), true, true},
		{"true", true, true},
		{"True", true, true},
		{"TRUE", true, true},
		{"t", true, true},
		{"T", true, true},
		{"1", true, true},
		{"false", false, true},
		{"F", false, true},
		{"0", false, true},
		{[]byte("true"), true, true},
		{named("f"), false, true},
		{int64(1), true, true},
		{int64(0), false, true},
		{1.0, true, true},
		{math.Copysign(0, -1), false, true},
		{uint8(1), true, true},
		{"yes", false, false},
		{"", false, false},
		{" true", false, false},
		{"tRUE", false, false},
		{int64(2), false, false},
		{0.5, false, false},
		{math.NaN(), false, false},
		{nil, false, false},
		{[]any{true}, false, false},
		{make(map[string]any), false, false},
		{point{}, false, false},
	}

	for _, tc := range tests {
		b, ok := Bool(tc.value)
		require.Equal(t, tc.ok, ok, "%#v", tc.value)
		require.Equal(t, tc.want, b, "%#v", tc.value)
	}
}

func TestNumber(t *testing.T) {
	// A Go number of any type keeps its exact value; text and other values aren't numbers.
	tests := []struct {
		value any
		want  any
	}{
		{5, int64(5)},
		{int8(-5), int64(-5)},
		{namedInt(7), int64(7)},
		{uint8(255), int64(255)},
		{uint64(math.MaxInt64), int64(math.MaxInt64)},
		{uint64(math.MaxUint64), uint64(math.MaxUint64)},
		{float32(0.5), 0.5},
		{float32(0.1), float64(float32(0.1))},
		{1.5, 1.5},
		{int64(9007199254740993), int64(9007199254740993)},
	}

	for _, tc := range tests {
		got, ok := Number(tc.value)
		require.True(t, ok, "%#v", tc.value)
		require.Equal(t, tc.want, got, "%#v", tc.value)
	}

	for _, v := range []any{"5", named("5"), true, nil, []int{5}, complex(1, 0)} {
		_, ok := Number(v)
		require.False(t, ok, "%#v", v)
	}
}

func TestClasses(t *testing.T) {
	// Byte slices are text, not lists; maps with keys of other kinds are other values, not objects.
	text, ok := Text(namedByte("ab"))
	require.True(t, ok)
	require.Equal(t, "ab", text)

	_, ok = List([]byte("ab"))
	require.False(t, ok, "a byte slice is text")

	items, ok := List([2]int{1, 2})
	require.True(t, ok)
	require.Equal(t, []any{1, 2}, items)

	items, ok = List([]string{"a"})
	require.True(t, ok)
	require.Equal(t, []any{"a"}, items)

	members, ok := Object(url.Values{"a": {"1"}})
	require.True(t, ok)
	require.Equal(t, map[string]any{"a": []string{"1"}}, members)

	_, ok = Object(map[int]string{1: "a"})
	require.False(t, ok)

	_, ok = Text(5)
	require.False(t, ok)
}

func TestEqualAndSame(t *testing.T) {
	// One rule compares values everywhere. Each row checks both orders: the rule is symmetric.
	nan := math.NaN()
	instant := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		a     any
		b     any
		equal bool
		same  bool
	}{
		{"a number is not the rune of its value", 49, "1", false, false},
		{"a fraction is not truncated", 1.5, int64(1), false, false},
		{"text reads as a number", "1", 1, true, false},
		{"numbers of any Go type compare by value", uint8(10), 10.0, true, true},
		{"a whole float equals the int", int64(5), 5.0, true, true},
		{"-0 is 0", math.Copysign(0, -1), 0, true, true},
		{"NaN equals nothing", nan, nan, false, false},
		{"infinities are equal", math.Inf(1), math.Inf(1), true, true},
		{"integers keep every digit", "9007199254740993", int64(9007199254740993), true, false},
		{"however they are written", "9.007199254740993e15", int64(9007199254740993), true, false},
		{"so they aren't the float64 nearest them", "9.007199254740993e15", int64(9007199254740992), false, false},
		{"above 2^53 a float is not the next integer", float64(1 << 53), int64(1<<53 + 1), false, false},
		{"text beyond int64 reads as a float", uint64(math.MaxUint64), "18446744073709551615", false, false},
		{"a float32 keeps its own value", float32(0.1), 0.1, false, false},
		{"bytes are text", []byte("10"), "10", true, true},
		{"bytes read as a number", []byte("10"), 10, true, false},
		{"text reads as a bool", "t", true, true, false},
		{"a number reads as a bool", 1, true, true, false},
		{"only 1 and 0 are bools", 2, true, false, false},
		{"text ParseBool can't read", "yes", true, false, false},
		{"nil is nil", nil, nil, true, true},
		{"nil is not false", nil, false, false, false},
		{"nil is not empty text", nil, "", false, false},
		{"nil is not an empty list", nil, []any(nil), false, false},
		{"a leading zero is not a number", "05", 5, false, false},
		{"a plus sign is not a number", "+5", 5, false, false},
		{"spaces are not a number", " 5", 5, false, false},
		{"5. is not a number", "5.", 5, false, false},
		{".5 is not a number", ".5", 0.5, false, false},
		{"hex is not a number", "0x5", 5, false, false},
		{"underscores are not a number", "1_0", 10, false, false},
		{"an exponent is a number", "1e3", 1000, true, false},
		{"a whole fraction is a number", "5.0", 5, true, false},
		{"text never reads as text of another kind", "a", []any{"a"}, false, false},
		{"lists compare by items", []string{"a"}, []any{"a"}, true, true},
		{"lists compare in order", []any{"a", "b"}, []any{"b", "a"}, false, false},
		{"lists of other lengths differ", []any{"a"}, []any{"a", "a"}, false, false},
		{"lists read their items", []any{"1", "t"}, []any{1, true}, true, false},
		{"objects compare by members", map[string]string{"a": "1"}, map[string]any{"a": int64(1)}, true, false},
		{"query values are an object of lists", url.Values{"a": {"1"}}, map[string]any{"a": []any{"1"}}, true, true},
		{"objects with other keys differ", map[string]any{"a": 1}, map[string]any{"b": 1}, false, false},
		{"objects of other sizes differ", map[string]any{"a": 1}, map[string]any{"a": 1, "b": 2}, false, false},
		{"structs compare deeply", point{1, "a"}, point{1, "a"}, true, true},
		{"a struct is not its pointer", point{1, "a"}, &point{1, "a"}, false, false},
		{"pointers compare their values", &point{1, "a"}, &point{1, "a"}, true, true},
		{"a time is its instant and location", instant, instant.In(time.FixedZone("x", 3600)), false, false},
		{"a struct is not text", point{}, "{0 }", false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.equal, Equal(tc.a, tc.b), "Equal(a, b)")
			require.Equal(t, tc.equal, Equal(tc.b, tc.a), "Equal(b, a)")
			require.Equal(t, tc.same, Same(tc.a, tc.b), "Same(a, b)")
			require.Equal(t, tc.same, Same(tc.b, tc.a), "Same(b, a)")
		})
	}
}

func TestCompare(t *testing.T) {
	// Order is exact, also between an int and a float, and text orders as the number it reads as.
	tests := []struct {
		a    any
		b    any
		want int
		ok   bool
	}{
		{1, 2, -1, true},
		{"10", 9, 1, true},
		{1.5, int64(1), 1, true},
		{"9007199254740993", float64(1 << 53), 1, true},
		{int64(math.MaxInt64), uint64(math.MaxInt64 + 1), -1, true},
		{uint64(math.MaxUint64), uint64(math.MaxUint64), 0, true},
		{math.Inf(1), math.MaxFloat64, 1, true},
		{math.Inf(-1), int64(math.MinInt64), -1, true},
		{math.Copysign(0, -1), 0, 0, true},
		{math.NaN(), 1, 0, false},
		{1, math.NaN(), 0, false},
		{"abc", 1, 0, false},
		{1, "abc", 0, false},
		{true, 1, 0, false},
		{nil, 1, 0, false},
	}

	for _, tc := range tests {
		got, ok := Compare(tc.a, tc.b)
		require.Equal(t, tc.ok, ok, "%#v vs %#v", tc.a, tc.b)
		require.Equal(t, tc.want, got, "%#v vs %#v", tc.a, tc.b)
	}
}

func TestLen(t *testing.T) {
	// Text counts bytes, lists items and objects members; other values have no length.
	tests := []struct {
		value any
		want  int
		ok    bool
	}{
		{"héllo", 6, true},
		{[]byte("ab"), 2, true},
		{[]any{1, 2, 3}, 3, true},
		{[]string(nil), 0, true},
		{map[string]any{"a": 1}, 1, true},
		{5, 0, false},
		{nil, 0, false},
		{true, 0, false},
		{point{}, 0, false},
	}

	for _, tc := range tests {
		got, ok := Len(tc.value)
		require.Equal(t, tc.ok, ok, "%#v", tc.value)
		require.Equal(t, tc.want, got, "%#v", tc.value)
	}
}

func TestDecodeJSON(t *testing.T) {
	// A document means the same through every reader: integers keep their digits, whole numbers are int64.
	doc, err := DecodeJSON([]byte(`{"big": 9007199254740993, "whole": 1.0, "half": 0.5, "list": [true, null, "x"]}`))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"big":   int64(9007199254740993),
		"whole": int64(1),
		"half":  0.5,
		"list":  []any{true, nil, "x"},
	}, doc)

	for doc, want := range map[string]string{
		`{"a": 1, "a": 2}`:   `duplicate object member name "a"`,
		"[\"\xff\"]":         "invalid UTF-8",
		`{"n": 1e400}`:       "number 1e400 is out of range",
		`[1] [2]`:            "a document holds one value, not more",
		`[1, nul]`:           `invalid character ']' in literal null`,
		`{"a": [{"b": }]}`:   "missing value after object name",
		`{"a": 1`:            "unexpected EOF",
		`{1: 2}`:             "object member name must be a string",
		``:                   "EOF",
		`["a", ]`:            "invalid character ',' at start of value",
		`{"a": 1, "b" 2}`:    "after object name (expecting ':')",
		`[1, 2, {"c": tru}]`: "in literal true",
	} {
		_, err := DecodeJSON([]byte(doc))
		require.ErrorContains(t, err, want, doc)
	}
}

func TestDecodeJSONLenient(t *testing.T) {
	// A request body is read the way a server would read it: duplicates, bad UTF-8 and huge numbers don't fail it.
	doc, err := DecodeJSONLenient([]byte("{\"a\": 1, \"a\": 2, \"s\": \"\xff\", \"n\": 1e400, \"m\": -1e400}"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": int64(2), "s": "�", "n": math.Inf(1), "m": math.Inf(-1)}, doc)

	_, err = DecodeJSONLenient([]byte(`{"a": }`))
	require.ErrorContains(t, err, "missing value after object name")
}

func TestViaJSON(t *testing.T) {
	// A value built in code means what its JSON means, in the data model of documents.
	got, err := ViaJSON(map[string]any{
		"header": map[string]string{"a": "b"},
		"n":      3,
		"f":      float32(0.5),
		"list":   []string{"x"},
		"point":  point{1, "a"},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"header": map[string]any{"a": "b"},
		"n":      int64(3),
		"f":      0.5,
		"list":   []any{"x"},
		"point":  map[string]any{"X": int64(1), "Y": "a"},
	}, got)

	_, err = ViaJSON(make(chan int))
	require.Error(t, err)

	_, err = ViaJSON(math.NaN())
	require.Error(t, err)
}

func TestPropParseNumber_Grammar(t *testing.T) {
	// V1: text is a number iff the JSON grammar allows it and its value fits a float64; then a whole number that
	// fits an int64 keeps every digit, however it is written, and anything else is the nearest float64, an int64
	// when whole.
	rapid.Check(t, func(t *rapid.T) {
		s := genNumberText().Draw(t, "text")

		want, wantOK := textNumber(s)
		got, ok := ParseNumber(s)

		require.Equal(t, wantOK, ok, "%q", s)
		require.Equal(t, want, got, "%q", s)
	})
}

func TestPropParseNumber_IntegersAnySpelling(t *testing.T) {
	// V10: an integer keeps every digit however it is written. Read as a float64 first, 9.007199254740993e15 and
	// 9007199254740993.0 were 9007199254740992, so an integer key, an order bound or Equal took the wrong number.
	rapid.Check(t, func(t *rapid.T) {
		n := genWideInt64().Draw(t, "n")
		s := genSpelling(n).Draw(t, "spelling")

		got, ok := ParseNumber(s)
		require.True(t, ok, s)
		require.Equal(t, n, got, s)

		doc, err := DecodeJSON([]byte(s))
		require.NoError(t, err)
		require.Equal(t, n, doc, s)

		require.True(t, Equal(s, n), s)

		if n < math.MaxInt64 {
			require.False(t, Equal(s, n+1), s)
		}
	})
}

func TestPropNumbers_CompareExactly(t *testing.T) {
	// V2: numbers of any Go types are equal when their exact values are, and order by them,
	// across int, uint and float types; NaN never compares.
	rapid.Check(t, func(t *rapid.T) {
		a, b := genGoNumber().Draw(t, "a"), genGoNumber().Draw(t, "b")

		want, ok := cmpExact(exactOf(a), exactOf(b))
		got, gotOK := Compare(a, b)

		require.Equal(t, ok, gotOK)
		require.Equal(t, want, got)
		require.Equal(t, ok && want == 0, Equal(a, b))
		require.Equal(t, ok && want == 0, Same(a, b))
	})
}

func TestPropEqual_Symmetric(t *testing.T) {
	// V3: the order of the values never matters; a value without NaN equals itself; and values that are the
	// same without reading text are also equal.
	rapid.Check(t, func(t *rapid.T) {
		a, b := genAny(2).Draw(t, "a"), genAny(2).Draw(t, "b")

		require.Equal(t, Equal(a, b), Equal(b, a))
		require.Equal(t, Same(a, b), Same(b, a))

		if Same(a, b) {
			require.True(t, Equal(a, b), "same implies equal")
		}

		if !hasNaN(a) {
			require.True(t, Equal(a, a), "%#v equals itself", a)
			require.True(t, Same(a, a), "%#v is the same as itself", a)
		}
	})
}

func TestPropEqual_NoRuneOrTruncation(t *testing.T) {
	// V4: a number never equals the character of its value, as reflect's conversions made 49 equal "1",
	// and a fraction never equals the integer it would truncate to.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(0, 0x10ffff).Draw(t, "n")
		require.False(t, Equal(n, string(rune(n))), "%d vs %q", n, string(rune(n)))

		f := rapid.Float64Range(-1e15, 1e15).Draw(t, "f")
		if f == math.Trunc(f) {
			f += 0.5
		}

		require.False(t, Equal(f, int64(math.Trunc(f))), "%v", f)

		c, ok := Compare(f, int64(math.Trunc(f)))
		require.True(t, ok)
		require.NotZero(t, c)
	})
}

func TestPropEqual_TextMeetsNumber(t *testing.T) {
	// V5: text equals a number iff it reads, as documents read it, as that exact number;
	// and a number or text equals a number iff they compare as equal.
	rapid.Check(t, func(t *rapid.T) {
		s := genNumberText().Draw(t, "text")
		n := genGoNumber().Draw(t, "number")

		read, ok := textNumber(s)

		want := false
		if ok {
			c, ordered := cmpExact(exactOf(read), exactOf(n))
			want = ordered && c == 0
		}

		require.Equal(t, want, Equal(s, n), "%q vs %#v", s, n)
		require.Equal(t, want, Equal(n, s), "%#v vs %q", n, s)
		require.False(t, Same(s, n), "Same never reads text")

		c, ordered := Compare(s, n)
		require.Equal(t, want, ordered && c == 0)
	})
}

func TestPropBool_ReadsAsParseBool(t *testing.T) {
	// V6: every bool reads the way strconv.ParseBool reads text: text as written, the numbers 1 and 0,
	// and a bool as itself; nothing else is a bool. Anything equals a bool exactly when it reads as that bool.
	rapid.Check(t, func(t *rapid.T) {
		v := genAny(1).Draw(t, "value")

		want, wantOK := boolOracle(v)
		got, ok := Bool(v)

		require.Equal(t, wantOK, ok, "%#v", v)
		require.Equal(t, want, got, "%#v", v)

		for _, b := range []bool{true, false} {
			require.Equal(t, wantOK && want == b, Equal(b, v), "%v vs %#v", b, v)
		}
	})
}

// boolOracle reads v as a bool, independently of Bool.
func boolOracle(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case namedBool:
		return bool(x), true
	case string:
		b, err := strconv.ParseBool(x)
		return b, err == nil
	case []byte:
		b, err := strconv.ParseBool(string(x))
		return b, err == nil
	case named:
		b, err := strconv.ParseBool(string(x))
		return b, err == nil
	}

	if _, isNumber := Number(v); isNumber {
		switch e := exactOf(v); {
		case e.nan || e.inf != 0:
			return false, false
		case e.r.Cmp(big.NewRat(1, 1)) == 0:
			return true, true
		case e.r.Sign() == 0:
			return false, true
		}
	}

	return false, false
}

func FuzzParseNumber(f *testing.F) {
	// V7: any text reads as a number only in the JSON grammar, without panicking.
	for _, s := range []string{"0", "-0", "1.5e3", "01", "+1", "1_000", "Inf", "9007199254740993", "1e400", "", "9.007199254740993e15", "0e99999999999999999999"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		n, ok := ParseNumber(s)
		if !ok {
			return
		}

		require.Regexp(t, _jsonNumber, s)

		switch x := n.(type) {
		case int64:
		case float64:
			require.False(t, math.IsInf(x, 0) || math.IsNaN(x), "%q", s)
		default:
			t.Fatalf("%q read as a %T", s, n)
		}

		// A whole number that fits an int64 is exactly it.
		if r, ok := exactRat(s); ok && r.IsInt() && r.Num().IsInt64() {
			require.Equal(t, r.Num().Int64(), n, "%q", s)
		}
	})
}

func FuzzEqual(f *testing.F) {
	// V8: any two JSON values compare in either order alike, and each equals itself, without panicking.
	for _, pair := range [][2]string{
		{`1`, `"1"`},
		{`1.0`, `"1.0"`},
		{`true`, `"t"`},
		{`[1, "2"]`, `["1", 2]`},
		{`{"a": 1}`, `{"a": "1"}`},
		{`null`, `false`},
		{`1e400`, `"1e400"`},
		{`9007199254740993`, `9007199254740992`},
	} {
		f.Add([]byte(pair[0]), []byte(pair[1]))
	}

	f.Fuzz(func(t *testing.T, x, y []byte) {
		a, err := DecodeJSONLenient(x)
		if err != nil {
			return
		}

		b, err := DecodeJSONLenient(y)
		if err != nil {
			return
		}

		require.Equal(t, Equal(a, b), Equal(b, a))
		require.Equal(t, Same(a, b), Same(b, a))
		require.True(t, Equal(a, a))
		require.True(t, Same(b, b))
	})
}

func FuzzDecodeJSON(f *testing.F) {
	// V9: a document the strict reader takes, the lenient reader takes too, with the same value.
	for _, doc := range []string{`{"a": [1, 2.5, "x", null, true]}`, `9007199254740993`, `{"a": 1, "a": 2}`, `"é"`, `[`} {
		f.Add([]byte(doc))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		strict, err := DecodeJSON(data)
		if err != nil {
			return
		}

		lenient, err := DecodeJSONLenient(data)
		require.NoError(t, err)
		require.True(t, Same(strict, lenient))
		require.Equal(t, strict, lenient)
	})
}

// Generators

// _jsonNumber is the JSON number grammar, written independently of the scanner it checks.
var _jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func toAny[T any](v T) any { return v }

// genNumberText draws text near the JSON number grammar: numbers in it, numbers spoiled the ways Go's parsers
// still accept, words those parsers know, and anything at all.
func genNumberText() *rapid.Generator[string] {
	spoilers := []func(string) string{
		func(s string) string { return "+" + s },
		func(s string) string { return "0" + s },
		func(s string) string { return " " + s },
		func(s string) string { return s + " " },
		func(s string) string { return s + "." },
		func(s string) string { return "." + s },
		func(s string) string { return s[:1] + "_" + s[1:] },
		func(s string) string { return "0x" + s },
		func(s string) string { return s + "e" },
		func(s string) string { return s + "e+" },
	}

	return rapid.OneOf(
		rapid.StringMatching(`-?(0|[1-9][0-9]{0,20})(\.[0-9]{1,8})?([eE][+-]?[0-9]{1,3})?`),
		rapid.Custom(func(t *rapid.T) string {
			s := rapid.StringMatching(`[1-9][0-9]{0,6}(\.[0-9]{1,3})?`).Draw(t, "number")
			return rapid.SampledFrom(spoilers).Draw(t, "spoiler")(s)
		}),
		rapid.SampledFrom([]string{"Inf", "-Inf", "NaN", "infinity", "1e400", "-1e400", "1e-400", "0x1p3", "", "-", "-0"}),
		rapid.String(),
	)
}

// textNumber is what ParseNumber should read s as: the grammar, then a whole number that fits an int64, exactly,
// however it is written, or the nearest float64, kept as an int64 when whole.
func textNumber(s string) (any, bool) {
	if !_jsonNumber.MatchString(s) {
		return nil, false
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false
	}

	if r, ok := exactRat(s); ok && r.IsInt() && r.Num().IsInt64() {
		return r.Num().Int64(), true
	}

	if f == math.Trunc(f) && f >= -(1<<63) && f < 1<<63 {
		return int64(f), true
	}

	return f, true
}

// exactRat reads s, a number in the JSON grammar, as its exact value. It refuses exponents beyond ±10000,
// whose powers of ten would take big.Rat too long.
func exactRat(s string) (*big.Rat, bool) {
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		if e, err := strconv.Atoi(s[i+1:]); err != nil || e > 10000 || e < -10000 {
			return nil, false
		}
	}

	return new(big.Rat).SetString(s)
}

// genSpelling draws a way to write n in the JSON number grammar: as its digits, with a zero fraction, or as a
// mantissa and an exponent, the mantissa with trailing zeros, the exponent with leading ones.
func genSpelling(n int64) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		digits, negative := strings.CutPrefix(strconv.FormatInt(n, 10), "-")

		sign := ""
		if negative {
			sign = "-"
		}

		// n is the mantissa times 10^shift: a positive shift moves the point left, a negative one appends zeros.
		lowest := -2
		if n == 0 {
			lowest = 0 // 00 isn't a number
		}

		shift := rapid.IntRange(lowest, len(digits)+2).Draw(t, "shift")

		var mantissa string

		switch {
		case shift < 0:
			mantissa = digits + strings.Repeat("0", -shift)
		case shift == 0:
			mantissa = digits
		case shift < len(digits):
			mantissa = digits[:len(digits)-shift] + "." + digits[len(digits)-shift:]
		default:
			mantissa = "0." + strings.Repeat("0", shift-len(digits)) + digits
		}

		if zeros := rapid.IntRange(0, 2).Draw(t, "trailing zeros"); zeros > 0 {
			if !strings.Contains(mantissa, ".") {
				mantissa += "."
			}

			mantissa += strings.Repeat("0", zeros)
		}

		if shift == 0 && rapid.Bool().Draw(t, "no exponent") {
			return sign + mantissa
		}

		exponent := strconv.Itoa(shift)
		if shift >= 0 {
			exponent = rapid.SampledFrom([]string{"", "+"}).Draw(t, "exponent sign") + exponent
		}

		padding := strings.Repeat("0", rapid.IntRange(0, 2).Draw(t, "exponent zeros"))
		if rest, ok := strings.CutPrefix(exponent, "-"); ok {
			exponent = "-" + padding + rest
		} else if rest, ok := strings.CutPrefix(exponent, "+"); ok {
			exponent = "+" + padding + rest
		} else {
			exponent = padding + exponent
		}

		return sign + mantissa + rapid.SampledFrom([]string{"e", "E"}).Draw(t, "e") + exponent
	})
}

// genWideInt64 draws an int64, often beyond 2^53, where a float64 no longer holds every integer.
func genWideInt64() *rapid.Generator[int64] {
	return rapid.OneOf(
		rapid.Int64(),
		rapid.Int64Range(1<<53, math.MaxInt64),
		rapid.Int64Range(math.MinInt64, -(1<<53)),
		rapid.SampledFrom([]int64{0, 1 << 53, 1<<53 + 1, math.MaxInt64, math.MinInt64, 9007199254740993}),
	)
}

// genGoNumber draws a number of any Go type, often near the edges where converting between types loses value.
func genGoNumber() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Map(rapid.Int64(), toAny),
		rapid.Map(rapid.Int64Range(-1<<54, 1<<54), toAny),
		rapid.Map(rapid.Int64Range(-1<<54, 1<<54), func(n int64) any { return float64(n) }),
		rapid.Map(rapid.Int8(), toAny),
		rapid.Map(rapid.Int16(), toAny),
		rapid.Map(rapid.Int32(), toAny),
		rapid.Map(rapid.Int(), toAny),
		rapid.Map(rapid.Uint64(), toAny),
		rapid.Map(rapid.Uint8(), toAny),
		rapid.Map(rapid.Uint(), toAny),
		rapid.Map(rapid.Float64(), toAny),
		rapid.Map(rapid.Float32(), toAny),
		rapid.Map(rapid.Float64Range(-4, 4), toAny),
		rapid.SampledFrom([]any{
			math.NaN(), math.Inf(1), math.Inf(-1), math.Copysign(0, -1), 0.0, int64(0), int64(1),
			float64(1 << 53), int64(1<<53 + 1), uint64(math.MaxUint64), float64(math.MaxUint64), uint64(1 << 63),
			int64(math.MaxInt64), float64(1 << 63), int64(math.MinInt64), float64(-1 << 63), float32(0.1), 0.1,
		}),
	)
}

// exactNumber is the exact value of a Go number: a rational, or ±Inf, or NaN.
type exactNumber struct {
	r   *big.Rat
	inf int
	nan bool
}

func exactOf(v any) exactNumber {
	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return exactNumber{r: new(big.Rat).SetInt64(rv.Int())}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return exactNumber{r: new(big.Rat).SetUint64(rv.Uint())}
	default:
		f := rv.Float()

		switch {
		case math.IsNaN(f):
			return exactNumber{nan: true}
		case math.IsInf(f, 0):
			return exactNumber{inf: int(math.Copysign(1, f))}
		default:
			return exactNumber{r: new(big.Rat).SetFloat64(f)}
		}
	}
}

// cmpExact orders two exact numbers. ok is false when one is NaN.
func cmpExact(a, b exactNumber) (int, bool) {
	switch {
	case a.nan || b.nan:
		return 0, false
	case a.inf != 0 || b.inf != 0:
		if a.inf == b.inf {
			return 0, true
		}

		if a.inf != 0 {
			return a.inf, true
		}

		return -b.inf, true
	default:
		return a.r.Cmp(b.r), true
	}
}

// genText draws text that often reads as a number or a bool.
func genText() *rapid.Generator[string] {
	return rapid.OneOf(
		genNumberText(),
		rapid.SampledFrom([]string{"true", "false", "t", "F", "TRUE", "True", "1", "0", "yes", "", " true", "tRUE"}),
		rapid.String(),
	)
}

// genAny draws a value of any class, the way documents, requests and Go code give them: data-model values,
// Go numbers of any type, byte slices, named types, typed lists and objects, structs, pointers and times.
// Floats inside structs are never NaN, since reflect.DeepEqual never finds a NaN equal to itself.
func genAny(depth int) *rapid.Generator[any] {
	scalars := rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), toAny),
		rapid.Map(rapid.Bool(), func(b bool) any { return namedBool(b) }),
		rapid.Map(genText(), toAny),
		rapid.Map(genText(), func(s string) any { return []byte(s) }),
		rapid.Map(genText(), func(s string) any { return named(s) }),
		genGoNumber(),
	)

	if depth == 0 {
		return scalars
	}

	return rapid.OneOf(
		scalars,
		rapid.Map(rapid.SliceOfN(genAny(depth-1), 0, 3), toAny),
		rapid.Map(rapid.SliceOfN(genText(), 0, 3), toAny),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-c]`), genAny(depth-1), 0, 3), toAny),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-c]`), genText(), 0, 3), toAny),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-c]`), rapid.SliceOfN(genText(), 0, 2), 0, 3), func(m map[string][]string) any {
			return url.Values(m)
		}),
		rapid.Custom(func(t *rapid.T) any {
			p := point{X: rapid.IntRange(-2, 2).Draw(t, "x"), Y: rapid.SampledFrom([]string{"", "a"}).Draw(t, "y")}
			if rapid.Bool().Draw(t, "pointer") {
				return &p
			}

			return p
		}),
		rapid.Map(rapid.Int64Range(0, 2), func(s int64) any { return time.Unix(s, 0).UTC() }),
	)
}

// hasNaN reports whether v holds a NaN, which equals nothing, itself included.
func hasNaN(v any) bool {
	if n, ok := Number(v); ok {
		f, isFloat := n.(float64)
		return isFloat && math.IsNaN(f)
	}

	if l, ok := List(v); ok && slices.ContainsFunc(l, hasNaN) {
		return true
	}

	if o, ok := Object(v); ok {
		for _, member := range o {
			if hasNaN(member) {
				return true
			}
		}
	}

	return false
}
