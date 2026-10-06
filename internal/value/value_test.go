package value

import (
	"math"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
