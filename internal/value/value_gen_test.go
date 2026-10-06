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
	"time"

	"pgregory.net/rapid"
)

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

// textNumber is what ParseNumber should read s as: the grammar, then an exact integer, or the nearest float64,
// kept as an int64 when whole.
func textNumber(s string) (any, bool) {
	if !_jsonNumber.MatchString(s) {
		return nil, false
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, false
	}

	if !strings.ContainsAny(s, ".eE") {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}

	if f == math.Trunc(f) && f >= -(1<<63) && f < 1<<63 {
		return int64(f), true
	}

	return f, true
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
