package matchbuild

import (
	"context"
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
)

func TestBuildMatcher_ChecksArguments(t *testing.T) {
	// A spec is checked when the mock loads: a wrong argument used to become a matcher that failed, panicked or
	// silently matched something else on every request.
	tests := []struct {
		spec []any
		err  string
	}{
		{[]any{"equal"}, "equal takes 1 argument, not 0"},
		{[]any{"equal", "a", "b"}, "equal takes 1 argument, not 2; write a list as one argument: [equal, [...]]"},
		{[]any{"present", "x"}, "present takes no argument, not 1"},
		{[]any{"isIn", "a", "b"}, "isIn takes 1 argument, a list of the items, not 2; write [isIn, [...]]"},
		{[]any{"isIn", "a"}, "isIn takes a list, not a string"},
		{[]any{"gt", "5"}, "gt takes a number, not a string; write it without quotes: 5"},
		{[]any{"gt", true}, "gt takes a number, not a bool"},
		{[]any{"gt", math.NaN()}, "gt takes finite numbers, not NaN"},
		{[]any{"equal", []any{math.Inf(1)}}, "equal takes finite numbers, not +Inf"},
		{[]any{"equal", map[string]any{"a": math.NaN()}}, "equal takes finite numbers, not NaN"},
		{[]any{"len", 2.5}, "len takes a length, a whole number from 0, not 2.5"},
		{[]any{"len", int64(-1)}, "len takes a length, a whole number from 0, not -1"},
		{[]any{"len", "3"}, "len takes a length, a whole number from 0, not a string; write it without quotes: 3"},
		{[]any{"item", int64(-1), "anything"}, "item takes an index, a whole number from 0, not -1"},
		{[]any{"item", int64(0)}, "item takes 2 arguments, not 1"},
		{[]any{"eqi", int64(1)}, `eqi takes a string, not a number; write it in quotes: "1"`},
		{[]any{"eqi", []any{}}, "eqi takes a string, not a list"},
		{[]any{"item", int64(0), int64(5)}, `item: a matcher is a name or a definition such as ["equal", 1], not a number`},
		{[]any{"jsonpath", "$.a", int64(5)}, `jsonpath: a matcher is a name or a definition such as ["equal", 1], not a number`},
		{[]any{"regex", "("}, "regex takes a regular expression, not \"(\": error parsing regexp: missing closing ): `(`"},
		{[]any{"eqj", "{"}, "eqj takes JSON: jsontext: unexpected EOF after offset 1"},
		{[]any{"jsonpath", int64(1), "anything"}, "jsonpath takes a JSON path first, not a number"},
		{[]any{"both", "anything"}, "both takes 2 matchers, not 1"},
		{[]any{"all", []any{}}, "all takes 1 matcher or more, not 0"},
		{[]any{"all", []any{[]any{"nope"}}}, `all: matcher 0: unknown matcher "nope"`},
		{[]any{"not", int64(1)}, `not: a matcher is a name or a definition such as ["equal", 1], not a number`},
		{[]any{int64(1), "x"}, `a matcher spec names its matcher first, as in ["equal", 1], not a number`},
		{[]any{"nope"}, `unknown matcher "nope"`},
		{[]any{"split", ",", int64(5)}, `split: a matcher is a name or a definition such as ["equal", 1], not a number`},
		// Each kind of matcher reports a missing argument.
		{[]any{"eqj"}, "eqj takes 1 argument, or 2 with options, not 0"},
		{[]any{"eqj", "{}", make(map[string]any), "x"}, "eqj takes 1 argument, or 2 with options, not 3"},
		{[]any{"eqj", "{}", "x"}, "eqj takes options, an object such as {ignore_array_order: ...}, not a string"},
		{[]any{"eqj", "{}", map[string]any{"ignore_order": true}}, `eqj takes the options ignore_array_order, ignore_extra_elements, not "ignore_order"`},
		{[]any{"eqj", "{}", map[string]any{"ignore_array_order": "yes"}}, "eqj takes true or false for ignore_array_order, not a string"},
		{[]any{"isIn"}, "isIn takes 1 argument, not 0"},
		{[]any{"glob"}, "glob takes 1 argument, not 0"},
		{[]any{"gt"}, "gt takes 1 argument, not 0"},
		{[]any{"len"}, "len takes 1 argument, not 0"},
		{[]any{"not"}, "not takes 1 argument, not 0"},
		{[]any{"urlpath"}, "urlpath takes 1 argument, not 0"},
		{[]any{"jsonpath", "$.a"}, "jsonpath takes 2 arguments, not 1"},
		{[]any{"split", ","}, "split takes 2 arguments, not 1"},
		{[]any{"either"}, "either takes 2 matchers, not 0"},
		{[]any{"hasexactly"}, "hasexactly takes 1 argument, not 0"},
		{[]any{"jsonschema"}, "jsonschema takes 1 argument, or 2 with options, not 0"},
		{[]any{"eqx"}, "eqx takes 1 argument, or 2 with options, not 0"},
		{[]any{"eqx", int64(1)}, "eqx takes an XML document, not a number"},
		{[]any{"eqx", "<a>"}, "eqx takes an XML document: match: eqx: element <a> is not closed"},
		{[]any{"eqx", "<a/>", map[string]any{"namespaces": "loose"}}, `eqx takes the namespaces uri, strict or none, not "loose"`},
		{[]any{"eqx", "<a/>", map[string]any{"ignore_child_order": int64(1)}}, "eqx takes true or false for ignore_child_order, not a number"},
		{[]any{"xpath", "/a"}, "xpath takes 2 arguments, or 3 with options, not 1"},
		{[]any{"before"}, "before takes 1 argument, or 2 with options, not 0"},
		{[]any{"before", int64(1)}, `before takes a date and time, or now, such as "now +3 days", not a number`},
		{[]any{"before", "tomorrow"}, `before takes a date and time, or now: "tomorrow" is not a date and time in RFC 3339, ISO 8601 or an HTTP format`},
		{[]any{"after", "now +3 fortnights"}, `after takes an offset in seconds, minutes, hours, days, weeks, months or years, not "fortnights"`},
		{[]any{"eqdt", "now", map[string]any{"truncate_actual": "first day of week"}}, `eqdt takes a truncation for truncate_actual, such as "first day of month", not "first day of week"`},
		{[]any{"eqdt", "now", map[string]any{"actual_format": ""}}, `eqdt takes a Go time layout for actual_format, such as "02/01/2006", not ""`},
		{[]any{"eqdt", "now", map[string]any{"truncate_last": "yes"}}, "eqdt takes true or false for truncate_last, not a string"},
		{[]any{"xpath", int64(1), "present"}, "xpath takes an XPath expression first, not a number"},
		{[]any{"xpath", "/a", int64(1)}, `xpath: a matcher is a name or a definition such as ["equal", 1], not a number`},
		{[]any{"xpath", "/a", "present", map[string]any{"namespaces": "p"}}, `xpath takes namespaces by prefix, such as {p: "urn:p"}, not a string`},
		{[]any{"xpath", "/a", "present", map[string]any{"namespaces": map[string]any{"p": int64(1)}}}, "xpath takes a namespace URI for p, not a number"},
		{[]any{"jsonschema", "{}", map[string]any{"draft": "5"}}, "jsonschema takes the draft 4, 6, 7, 2019-09 or 2020-12, not a string"},
		{[]any{"jsonschema", "{}", map[string]any{"draft": 7.5}}, "jsonschema takes the draft 4, 6, 7, 2019-09 or 2020-12, not a number"},
		{[]any{"hasexactly", []any{int64(1)}}, `hasexactly: matcher 0: a matcher is a name or a definition such as ["equal", 1], not a number`},
	}

	for _, tc := range tests {
		_, err := _registry.BuildMatcher(tc.spec)
		require.EqualError(t, err, tc.err, "%v", tc.spec)
	}
}

func TestBuildMatcher_URLPathTakesAPattern(t *testing.T) {
	// [urlpath, pattern] globs the path; [urlpath, matcher], as descriptions write it, matches the path itself.
	m, err := _registry.BuildMatcher([]any{"urlpath", "/a/*"})
	require.NoError(t, err)
	require.True(t, matches(t, m, "/a/b"))
	require.False(t, matches(t, m, "/b"))
}

func TestBuildMatcher_EqualTakesNull(t *testing.T) {
	// [equal, null] matches nil, unlike [equal], which names no value.
	m, err := _registry.BuildMatcher([]any{"equal", nil})
	require.NoError(t, err)
	require.True(t, matches(t, m, nil))
	require.False(t, matches(t, m, ""))
}

func TestBuildMatcher_LoadsWhatMatchersDescribe(t *testing.T) {
	// A mock describes its matchers in the format of mock files, so a description loads back, regexp included.
	for _, m := range []vlatte.Matcher{
		match.RegExpr("^a"), match.IsIn([]any{"a", int64(1)}), match.GreaterThan(int64(1) << 60), match.Len(2),
		match.Item(0, match.Equal("a")), match.Field("$.a", match.Truthy()), match.Split(",", match.Len(2)),
		match.Both(match.Present(), match.Not(match.Nil())), match.All(match.HasPrefix("a"), match.HasSuffix("b")),
		match.EqualJSON(`{"a": 1}`), match.EqualJSON(`[1]`, match.IgnoreArrayOrder(), match.IgnoreExtraElements()),
		match.StrictEqual(int64(1)), match.JSONSchema(`{"type": "string"}`),
		match.JSONSchema(`{}`, match.SchemaDraft(match.JSONSchemaDraft7)),
		match.EqualXML(`<a/>`), match.EqualXML(`<a/>`, match.IgnoreChildOrder(), match.XMLNamespaces(match.XMLNamespacesIgnored)),
		match.XPath("/a", match.Equal("1")), match.XPath("/p:a", match.Present(), match.XPathNamespace("p", "urn:p")),
		match.Before(time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC)), match.BeforeNow(match.ExpectedOffset(-3, match.Days)),
		match.After(time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC), match.LocalTime(), match.ExpectedOffset(2, match.Weeks)),
		match.EqualDateTimeNow(match.TruncateExpected(match.FirstDayOfMonth), match.TruncateActual(match.LastDayOfYear),
			match.TruncateLast(), match.ActualFormat("02/01/2006"), match.LocalTime()), match.URLPath("/a/*"),
		match.RequestURI("/items?page=2&a=%2F"), match.ItemsMatch([]any{match.HasPrefix("a"), int64(2)}),
	} {
		desc, ok := descriptor.IsSelfDescriptive(m)
		require.True(t, ok)

		data, err := desc.MarshalJSON()
		require.NoError(t, err)

		spec, err := value.DecodeJSON(data)
		require.NoError(t, err)

		loaded, err := _registry.BuildMatcher(spec)
		require.NoError(t, err, "%s", data)

		// The same matcher loads back, options included: it describes itself the same.
		loadedDesc, ok := descriptor.IsSelfDescriptive(loaded)
		require.True(t, ok)

		loadedData, err := loadedDesc.MarshalJSON()
		require.NoError(t, err)
		require.JSONEq(t, string(data), string(loadedData))
	}
}

// _allowed lists the argument counts each matcher takes, at most 3.
var _allowed = map[string][]int{
	"anything": {0}, "empty": {0}, "falsy": {0}, "nil": {0}, "present": {0}, "truthy": {0},
	"equal": {1}, "eqs": {1}, "contains": {1}, "eqj": {1, 2}, "jsonschema": {1, 2}, "eqx": {1, 2},
	"before": {1, 2}, "after": {1, 2}, "eqdt": {1, 2}, "isin": {1}, "itemsmatch": {1}, "eqi": {1}, "glob": {1},
	"hasprefix": {1}, "hassuffix": {1}, "regex": {1}, "haskey": {1}, "gt": {1}, "gte": {1}, "lt": {1}, "lte": {1},
	"len": {1}, "each": {1}, "some": {1}, "not": {1}, "trim": {1}, "lowercase": {1}, "uppercase": {1}, "urlpath": {1},
	"requesturi": {1},
	"hasexactly": {1, 2, 3},
	"xpath":      {2, 3},
	"item":       {2}, "jsonpath": {2}, "split": {2}, "both": {1, 2}, "either": {1, 2}, "xor": {1, 2}, "all": {1, 2, 3}, "any": {1, 2, 3},
}

// genArg draws an argument of a spec: values of every class, matcher names and specs, and numbers at the edges.
func genArg() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.SampledFrom([]any{
			nil, true, "a", "5", "^a", "$.a", "$[", "(", `{"a": 1}`, ",", "/a", "anything", "present",
			int64(0), int64(1), int64(-1), int64(3), int64(1) << 40, 1.5, 2.0, -0.5, math.NaN(), math.Inf(1),
			[]any{},
			[]any{"a", int64(1)},
			[]any{"equal", "a"},
			[]any{[]any{"present"}, []any{"nil"}},
			map[string]any{"a": int64(1)},
		}),
	)
}

func TestPropBuildMatcher_Arguments(t *testing.T) {
	// X6: an order matcher builds exactly when its bound is a finite number; len and item, when their count is a
	// whole number from 0; and no spec builds with an argument count its matcher doesn't take.
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom([]string{"gt", "gte", "lt", "lte", "len"}).Draw(t, "order or len")
		arg := genArg().Draw(t, "arg")

		_, err := _registry.BuildMatcher([]any{name, arg})

		want := false

		switch n := arg.(type) {
		case int64:
			want = name != "len" || n >= 0
		case float64:
			want = name != "len" && !math.IsNaN(n) && !math.IsInf(n, 0)
		}

		require.Equal(t, want, err == nil, "[%s, %#v]: %v", name, arg, err)

		_, err = _registry.BuildMatcher([]any{"item", arg, "anything"})
		index, isInt := arg.(int64)
		require.Equal(t, isInt && index >= 0, err == nil, "[item, %#v]: %v", arg, err)

		pick := rapid.SampledFrom(slices.Sorted(maps.Keys(_allowed))).Draw(t, "name")
		args := rapid.SliceOfN(genArg(), 0, 3).Draw(t, "args")

		if _, err := _registry.BuildMatcher(append([]any{pick}, args...)); err == nil {
			require.Contains(t, _allowed[pick], len(args), "%s built with %d arguments", pick, len(args))
		}
	})
}

func FuzzBuildAndMatch(f *testing.F) {
	// No spec makes the builder panic, and no matcher a spec builds fails or panics on any value:
	// the value as JSON, as text, or as bytes.
	for _, seed := range [][2]string{
		{`["equal", 1]`, `"1"`},
		{`["jsonpath", "$.a", ["gt", 1.5]]`, `{"a": 2}`},
		{`["all", [["present"], ["len", 3]]]`, `"abc"`},
		{`["item", 1, ["isIn", [1, "x"]]]`, `[0, "x"]`},
		{`["split", ",", ["itemsMatch", ["a", "b"]]]`, `"b,a"`},
		{`["eqj", {"a": [1, 2]}]`, `{"a": [1, 2.0]}`},
		{`["eqj", [1, {"a": 2}], {"ignore_array_order": true, "ignore_extra_elements": true}]`, `[{"a": 2, "b": 3}, 0, 1]`},
		{`["regexp", "^a"]`, `"abc"`},
		{`["xor", "truthy", "falsy"]`, `1`},
		{`["hasexactly", [["equal", 1], ["contains", "b"]]]`, `["abc", 1]`},
		{`["jsonschema", {"type": "object", "required": ["a"]}, {"draft": 7}]`, `{"a": 1}`},
		{`["eqx", "<a><b>1</b><b>2</b></a>", {"ignore_child_order": true}]`, `"<a><b>2</b><b>1</b></a>"`},
		{`["xpath", "count(//b)", ["gt", 1]]`, `"<a><b>2</b><b>1</b></a>"`},
		{`["before", "now +1 days", {"truncate_expected": "first day of month"}]`, `"2021-06-01T12:00:00Z"`},
		{`["eqdt", "2021-06-01", {"actual_format": "02/01/2006"}]`, `"01/06/2021"`},
	} {
		f.Add([]byte(seed[0]), []byte(seed[1]))
	}

	f.Fuzz(func(t *testing.T, specData, valueData []byte) {
		spec, err := value.DecodeJSONLenient(specData)
		if err != nil {
			return
		}

		m, err := _registry.TryBuildMatcher(spec)
		if err != nil {
			return
		}

		v, err := value.DecodeJSONLenient(valueData)
		if err != nil {
			v = nil
		}

		for _, given := range []any{v, string(valueData), valueData} {
			_, err := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), given))
			require.NoError(t, err)
		}
	})
}
