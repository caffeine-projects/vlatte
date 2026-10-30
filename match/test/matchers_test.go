package test

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"math"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
)

// _scalars mixes values that Equal reads into each other, and near misses: numbers of several Go types, text that
// reads as a number or a bool, and text that doesn't.
var _scalars = []any{
	nil, true, false, "t", "true", "0", "1", "1.0", "01", "a", "", 1, int64(1), 1.0, 1.5, uint8(1), float32(1.5),
	49, "49", 2, "2", int64(0), math.Copysign(0, -1), []byte("1"),
}

func matches(t require.TestingT, m vlatte.Matcher, v any) bool {
	ok, err := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
	require.NoError(t, err)

	return ok
}

func TestPropIsIn_EqualsAnItem(t *testing.T) {
	// X1: IsIn matches exactly the values Equal finds among its items, and Contain finds an item the same way.
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		v := genScalar().Draw(t, "value")

		want := slices.ContainsFunc(items, func(item any) bool { return value.Equal(item, v) })

		require.Equal(t, want, matches(t, match.IsIn(items), v))
		require.Equal(t, want, matches(t, match.Contain(v), items))
	})
}

func TestPropContain_Text(t *testing.T) {
	// X1: text holds expected text, and nothing else: no number or bool is read out of text, or into it.
	rapid.Check(t, func(t *rapid.T) {
		text := rapid.StringMatching(`[a1. ]{0,6}`).Draw(t, "text")
		expected := genScalar().Draw(t, "expected")

		want := false
		if s, ok := value.Text(expected); ok {
			want = len(s) <= len(text) && slices.ContainsFunc(substrings(text), func(sub string) bool { return sub == s })
		}

		require.Equal(t, want, matches(t, match.Contain(expected), text), "%#v in %q", expected, text)
	})
}

// substrings lists every substring of s, the empty one included.
func substrings(s string) []string {
	out := []string{""}
	for i := range len(s) {
		for j := i + 1; j <= len(s); j++ {
			out = append(out, s[i:j])
		}
	}

	return out
}

// permutes reports whether some ordering of got pairs each of its items with the item of want at the same index.
func permutes(want, got []any) bool {
	if len(want) != len(got) {
		return false
	}

	if len(want) == 0 {
		return true
	}

	for i := range got {
		if value.Equal(want[0], got[i]) {
			rest := slices.Delete(slices.Clone(got), i, i+1)
			if permutes(want[1:], rest) {
				return true
			}
		}
	}

	return false
}

func TestPropItemsMatch_SomeOrderPairsUp(t *testing.T) {
	// X2: ItemsMatch matches a list exactly when some order of it pairs each item with an equal item of its own.
	// Equal isn't transitive, as "1.0" and "1" both equal 1 but not each other, so a greedy pairing would miss some.
	rapid.Check(t, func(t *rapid.T) {
		items := genItems().Draw(t, "items")
		got := genItems().Draw(t, "value")

		if rapid.Bool().Draw(t, "shuffle the items") {
			got = slices.Clone(items)
			for i := len(got) - 1; i > 0; i-- {
				j := rapid.IntRange(0, i).Draw(t, "swap")
				got[i], got[j] = got[j], got[i]
			}
		}

		require.Equal(t, permutes(items, got), matches(t, match.ItemsMatch(items), got), "%#v vs %#v", items, got)
	})
}

func TestPropStrictEqualAndEqualJSON(t *testing.T) {
	// X3: StrictEqual is value.Same, and EqualJSON compares JSON by the same rule, whether each side is JSON text
	// or a value written as JSON. A string expectation, or a text value, is JSON text.
	rapid.Check(t, func(t *rapid.T) {
		a, b := genDoc(2).Draw(t, "a"), genDoc(2).Draw(t, "b")
		want := value.Same(a, b)

		require.Equal(t, want, matches(t, match.StrictEqual(a), b))

		textA, err := json.Marshal(a)
		require.NoError(t, err)

		textB, err := json.Marshal(b)
		require.NoError(t, err)

		require.Equal(t, want, matches(t, match.EqualJSON(string(textA)), string(textB)), "%s vs %s", textA, textB)

		if _, isText := b.(string); !isText {
			require.Equal(t, want, matches(t, match.EqualJSON(string(textA)), b), "%s vs %#v", textA, b)
		}

		if _, isText := a.(string); !isText {
			require.Equal(t, want, matches(t, match.EqualJSON(a), string(textB)), "%#v vs %s", a, textB)
		}
	})
}

func TestPropOrder_ComparesExactly(t *testing.T) {
	// X4: the order matchers agree with value.Compare, whatever the Go type of their bound, and on numbers
	// greater-or-equal is greater or Equal. A value that isn't a number, or text reading as one, matches none.
	rapid.Check(t, func(t *rapid.T) {
		v := genScalar().Draw(t, "value")

		var (
			gt, gte, lt, lte vlatte.Matcher
			bound            any
		)

		if rapid.Bool().Draw(t, "integer bound") {
			b := rapid.Int64Range(-2, 50).Draw(t, "bound")
			gt, gte, lt, lte, bound = match.GreaterThan(b), match.GTE(b), match.LessThan(b), match.LTE(b), b
		} else {
			b := rapid.SampledFrom([]float64{-0.5, 0, 1, 1.5, 49, math.Inf(1), math.NaN()}).Draw(t, "bound")
			gt, gte, lt, lte, bound = match.GreaterThan(b), match.GTE(b), match.LessThan(b), match.LTE(b), b
		}

		c, ok := value.Compare(v, bound)

		require.Equal(t, ok && c > 0, matches(t, gt, v), "%#v > %#v", v, bound)
		require.Equal(t, ok && c >= 0, matches(t, gte, v), "%#v >= %#v", v, bound)
		require.Equal(t, ok && c < 0, matches(t, lt, v), "%#v < %#v", v, bound)
		require.Equal(t, ok && c <= 0, matches(t, lte, v), "%#v <= %#v", v, bound)

		if _, isNumber := value.Number(v); isNumber {
			require.Equal(t, matches(t, gt, v) || value.Equal(v, bound), matches(t, gte, v))
		}
	})
}

// point is a struct value a matcher may be given from code.
type point struct {
	X int
	Y string
}

func TestPropMatchers_NoErrorOnAnyValue(t *testing.T) {
	// X5: no built-in matcher returns an error or panics, whatever value it gets: a value it can't take doesn't
	// match. An error would stop the search for a mock, so it's left to user code, such as Function.
	matchers := []vlatte.Matcher{
		match.Equal(int64(1)), match.StrictEqual("a"), match.EqualJSON(`{"a": 1}`), match.EqualIgnoreCase("a"), match.IsIn([]any{int64(1), "a"}),
		match.Contain("a"), match.ItemsMatch([]any{int64(1)}), match.GreaterThan(1), match.GTE(1.5), match.LessThan(uint8(2)), match.LTE(int64(-1)),
		match.Truthy(), match.Falsy(), match.Len(1), match.Empty(), match.Present(), match.Nil(), match.Anything(), match.Item(0, match.Equal(int64(1))), match.Each(match.Equal("a")),
		match.Some(match.Equal("a")), match.JSONPath("$.a", match.Equal(int64(1))), match.HasKey("$.a"), match.GlobMatch("a*"), match.HasPrefix("a"), match.HasSuffix("a"),
		match.RegExpr("^a"), match.Trim(match.Equal("a")), match.ToLower(match.Equal("a")), match.ToUpper(match.Equal("A")), match.Split(",", match.Len(2)), match.URLPath("/a"),
		match.Not(match.Equal(int64(1))), match.All(match.Equal("a"), match.Truthy()), match.Any(match.Len(1), match.Empty()), match.Both(match.Present(), match.Nil()),
		match.Either(match.Truthy(), match.Falsy()), match.XOR(match.Truthy(), match.Falsy()),
		match.EqualJSON(`[1, {"a": 2}]`, match.IgnoreArrayOrder(), match.IgnoreExtraElements()), match.ItemsMatch([]vlatte.Matcher{match.Equal("a"), match.Len(1)}),
		match.JSONSchema(`{"type": "object", "required": ["a"]}`), match.EqualXML(`<a b="1"><c/></a>`, match.IgnoreChildOrder()),
		match.XPath("//a", match.Some(match.Equal("1"))), match.XPath("count(//a)", match.GreaterThan(1)),
		match.Before(time.Unix(0, 0)), match.AfterNow(match.ExpectedOffset(-1, match.Days), match.TruncateActual(match.FirstDayOfMonth)),
		match.EqualDateTime(time.Unix(0, 0), match.ActualFormat("02/01/2006"), match.LocalTime()),
	}

	rapid.Check(t, func(t *rapid.T) {
		m := rapid.SampledFrom(matchers).Draw(t, "matcher")
		v := genAnyValue().Draw(t, "value")

		_, err := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
		require.NoError(t, err, "%s on %#v", m.Name(), v)
	})
}

func TestTextMatchers_ValuesThatArentText(t *testing.T) {
	// A text matcher doesn't match a value that isn't text, and it isn't an error: an error would stop the search.
	for _, m := range []vlatte.Matcher{
		match.EqualIgnoreCase("a"), match.GlobMatch("*"), match.HasPrefix(""), match.HasSuffix(""), match.RegExpr(".*"), match.Trim(match.Anything()),
		match.ToLower(match.Anything()), match.ToUpper(match.Anything()), match.Split(",", match.Anything()), match.URLPath("*"), match.HasKey("$.a"),
	} {
		for _, v := range []any{5, nil, true, []any{"a"}, map[string]any{"a": "b"}} {
			ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
			require.NoError(t, err, "%s on %#v", m.Name(), v)

			if _, isObject := v.(map[string]any); m.Name() == "HasKey" && isObject {
				continue // a decoded object is JSON already
			}

			require.False(t, ok, "%s on %#v", m.Name(), v)
		}
	}
}

func TestCombinators_PassOnErrorsOfUserCode(t *testing.T) {
	// Errors come only from user code now, and they still stop the search: they are bugs to fix.
	errMatcher := errors.New("test: matcher unavailable")
	boom := match.Function(func(any) (bool, error) { return false, errMatcher })

	for m, v := range map[vlatte.Matcher]any{
		match.Each(boom):                   []any{1},
		match.Item(0, boom):                []any{1},
		match.Some(boom):                   []any{1},
		match.URLPathMatch(boom):           "/a",
		match.Trim(boom):                   "a",
		match.ToLower(boom):                "a",
		match.ToUpper(boom):                "a",
		match.Split(",", boom):             "a",
		match.JSONPath("$", boom):          "{}",
		match.Both(match.Anything(), boom): "a",
		match.Not(boom):                    "a",
		match.XOR(match.Anything(), boom):  "a",
	} {
		_, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.ErrorIs(t, err, errMatcher, m.Name())
	}
}

func TestCombinators_AUserPanicBecomesAMatchingError(t *testing.T) {
	panicking := match.Function(func(any) (bool, error) { panic("broken matcher") })
	tests := []struct {
		name    string
		matcher vlatte.Matcher
		want    string
	}{
		{name: "all", matcher: match.All(match.Anything(), panicking), want: "all: matcher[1]: panic: broken matcher"},
		{name: "any", matcher: match.Any(match.Nil(), panicking), want: "any: matcher[1]: panic: broken matcher"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := tt.matcher.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "tea"))
			require.False(t, ok)
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestCombinators_DescriptionsTolerateUserMatchersWithoutADescription(t *testing.T) {
	user := match.Function(func(any) (bool, error) { return true, nil })
	tests := []struct {
		name    string
		matcher vlatte.Matcher
		want    string
	}{
		{name: "not", matcher: match.Not(user), want: `["not",["anything"]]`},
		{name: "each", matcher: match.Each(user), want: `["each"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc, ok := vlatte.IsSelfDescriptive(tt.matcher)
			require.True(t, ok)
			data, err := desc.MarshalJSON()
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(data))
		})
	}
}

func TestMatchers_ATypedNilPointerIsAbsent(t *testing.T) {
	var pointer *point
	require.False(t, matches(t, match.Present(), pointer))
	require.True(t, matches(t, match.Not(match.Present()), pointer))
}

// Generators

func genScalar() *rapid.Generator[any] {
	return rapid.SampledFrom(_scalars)
}

func genItems() *rapid.Generator[[]any] {
	return rapid.SliceOfN(genScalar(), 0, 5)
}

// genDoc draws a JSON document in the data model, with numbers that are whole, fractional and large.
func genDoc(depth int) *rapid.Generator[any] {
	scalars := rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.StringMatching(`[a1.]{0,3}`), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-10, 10), func(f float64) any { return f }),
	)

	if depth == 0 {
		return scalars
	}

	return rapid.OneOf(
		scalars,
		rapid.Map(rapid.SliceOfN(genDoc(depth-1), 0, 3), func(l []any) any { return l }),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[ab]`), genDoc(depth-1), 0, 2), func(m map[string]any) any { return m }),
	)
}

// genAnyValue draws a value of any class a matcher may be given: from documents, requests or code.
func genAnyValue() *rapid.Generator[any] {
	return rapid.OneOf(
		genScalar(),
		rapid.SampledFrom([]any{
			`{"a": 1}`, `[1, 2]`, "/a/b?c=d", "http://x/a", "%zz", "a,b", " A ", "A", "aaa",
			[]string{"1", "2"},
			[]any{int64(1), "a", nil},
			[]any{},
			map[string]any{"a": int64(1)},
			map[string]string{"a": "b"},
			url.Values{"a": {"1"}},
			point{1, "a"},
			&point{}, time.Unix(0, 0), make(chan int), &url.URL{Path: "/a"},
			[2]int{1, 2},
			math.NaN(), math.Inf(-1), uint64(math.MaxUint64),
		}),
	)
}
