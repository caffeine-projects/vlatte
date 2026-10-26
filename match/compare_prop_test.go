package match

import (
	"context"
	json "encoding/json/v2"
	"math"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// _scalars mixes values that Equal reads into each other, and near misses: numbers of several Go types, text that
// reads as a number or a bool, and text that doesn't.
var _scalars = []any{
	nil, true, false, "t", "true", "0", "1", "1.0", "01", "a", "", 1, int64(1), 1.0, 1.5, uint8(1), float32(1.5),
	49, "49", 2, "2", int64(0), math.Copysign(0, -1), []byte("1"),
}

func genScalar() *rapid.Generator[any] {
	return rapid.SampledFrom(_scalars)
}

func genItems() *rapid.Generator[[]any] {
	return rapid.SliceOfN(genScalar(), 0, 5)
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

		require.Equal(t, want, matches(t, IsIn(items), v))
		require.Equal(t, want, matches(t, Contain(v), items))
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

		require.Equal(t, want, matches(t, Contain(expected), text), "%#v in %q", expected, text)
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

		require.Equal(t, permutes(items, got), matches(t, ItemsMatch(items), got), "%#v vs %#v", items, got)
	})
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

func TestPropStrictEqualAndEqualJSON(t *testing.T) {
	// X3: StrictEqual is value.Same, and EqualJSON compares JSON by the same rule, whether each side is JSON text
	// or a value written as JSON. A string expectation, or a text value, is JSON text.
	rapid.Check(t, func(t *rapid.T) {
		a, b := genDoc(2).Draw(t, "a"), genDoc(2).Draw(t, "b")
		want := value.Same(a, b)

		require.Equal(t, want, matches(t, StrictEqual(a), b))

		textA, err := json.Marshal(a)
		require.NoError(t, err)

		textB, err := json.Marshal(b)
		require.NoError(t, err)

		require.Equal(t, want, matches(t, EqualJSON(string(textA)), string(textB)), "%s vs %s", textA, textB)

		if _, isText := b.(string); !isText {
			require.Equal(t, want, matches(t, EqualJSON(string(textA)), b), "%s vs %#v", textA, b)
		}

		if _, isText := a.(string); !isText {
			require.Equal(t, want, matches(t, EqualJSON(a), string(textB)), "%#v vs %s", a, textB)
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
			gt, gte, lt, lte, bound = GreaterThan(b), GTE(b), LessThan(b), LTE(b), b
		} else {
			b := rapid.SampledFrom([]float64{-0.5, 0, 1, 1.5, 49, math.Inf(1), math.NaN()}).Draw(t, "bound")
			gt, gte, lt, lte, bound = GreaterThan(b), GTE(b), LessThan(b), LTE(b), b
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

func TestPropMatchers_NoErrorOnAnyValue(t *testing.T) {
	// X5: no built-in matcher returns an error or panics, whatever value it gets: a value it can't take doesn't
	// match. An error would stop the search for a mock, so it's left to user code, such as Function.
	matchers := []vlatte.Matcher{
		Equal(int64(1)), StrictEqual("a"), EqualJSON(`{"a": 1}`), EqualIgnoreCase("a"), IsIn([]any{int64(1), "a"}),
		Contain("a"), ItemsMatch([]any{int64(1)}), GreaterThan(1), GTE(1.5), LessThan(uint8(2)), LTE(int64(-1)),
		Truthy(), Falsy(), Len(1), Empty(), Present(), Nil(), Anything(), Item(0, Equal(int64(1))), Each(Equal("a")),
		Some(Equal("a")), JSONPath("$.a", Equal(int64(1))), HasKey("$.a"), GlobMatch("a*"), HasPrefix("a"), HasSuffix("a"),
		RegExpr("^a"), Trim(Equal("a")), ToLower(Equal("a")), ToUpper(Equal("A")), Split(",", Len(2)), URLPath("/a"),
		Not(Equal(int64(1))), All(Equal("a"), Truthy()), Any(Len(1), Empty()), Both(Present(), Nil()),
		Either(Truthy(), Falsy()), XOR(Truthy(), Falsy()),
		EqualJSON(`[1, {"a": 2}]`, IgnoreArrayOrder(), IgnoreExtraElements()), ItemsMatch([]vlatte.Matcher{Equal("a"), Len(1)}),
		JSONSchema(`{"type": "object", "required": ["a"]}`), EqualXML(`<a b="1"><c/></a>`, IgnoreChildOrder()),
		XPath("//a", Some(Equal("1"))), XPath("count(//a)", GreaterThan(1)),
		Before(time.Unix(0, 0)), AfterNow(ExpectedOffset(-1, Days), TruncateActual(FirstDayOfMonth)),
		EqualDateTime(time.Unix(0, 0), ActualFormat("02/01/2006"), LocalTime()),
	}

	rapid.Check(t, func(t *rapid.T) {
		m := rapid.SampledFrom(matchers).Draw(t, "matcher")
		v := genAnyValue().Draw(t, "value")

		_, err := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
		require.NoError(t, err, "%s on %#v", m.Name(), v)
	})
}
