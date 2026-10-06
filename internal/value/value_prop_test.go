package value

import (
	"math"
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

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
