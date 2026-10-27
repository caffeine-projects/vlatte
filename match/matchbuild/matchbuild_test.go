package matchbuild

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
)

// _registry builds the specs of the tests: the built-in matchers only.
var _registry = MustNew()

func matches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()
	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)
	return ok
}

// TryBuildMatcher

func TestTryBuildMatcher_NonSliceBecomesEqual(t *testing.T) {
	m, err := _registry.TryBuildMatcher("hello")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "hello"))
	assert.False(t, matches(t, m, "world"))
}

func TestTryBuildMatcher_SliceBuildsNamed(t *testing.T) {
	m, err := _registry.TryBuildMatcher([]any{"contains", "world"})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "hello world"))
	assert.False(t, matches(t, m, "hello"))
}

func TestTryBuildMatcher_NilReturnsError(t *testing.T) {
	_, err := _registry.TryBuildMatcher(nil)
	require.Error(t, err)
}

// BuildMatcher

func TestBuildMatcher_StringBuildsNoArgMatcher(t *testing.T) {
	m, err := _registry.BuildMatcher("anything")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "whatever"))
}

func TestBuildMatcher_ArrayBuildsNamed(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"eq", "test"})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "test"))
	assert.False(t, matches(t, m, "other"))
}

func TestBuildMatcher_UnknownKeyReturnsError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"nonexistent_matcher", "arg"})
	require.Error(t, err)
}

func TestBuildMatcher_EmptyArrayReturnsError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{})
	require.Error(t, err)
}

// Split — argument order: [separator, matcher]

func TestBuildMatcher_Split(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"split", ",", []any{"contains", "b"}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "a,b,c"))
	assert.False(t, matches(t, m, "a,c,d"))
}

func TestBuildMatcher_SplitWrongArgCount(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"split", []any{","}}) // only separator, missing matcher
	require.EqualError(t, err, "split takes 2 arguments, not 1")
}

func TestBuildMatcher_SplitFirstArgNotString(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"split", int64(42), []any{"anything"}})
	require.EqualError(t, err, `split takes a separator first, not a number; write it in quotes: "42"`)
}

// Numeric matchers — error messages must use %v (not %d)

func TestBuildMatcher_GreaterThan(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"gt", 5.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 6))
	assert.False(t, matches(t, m, 4))
}

func TestBuildMatcher_GreaterThanError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"gt", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThan(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"lt", 10.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 5))
	assert.False(t, matches(t, m, 15))
}

func TestBuildMatcher_LessThanError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"lt", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
	assert.True(t, strings.Contains(err.Error(), "less") || strings.Contains(err.Error(), "lt"), "error should name 'lt': %s", err)
}

func TestBuildMatcher_GreaterThanOrEqual(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"gte", 5.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 5))
	assert.False(t, matches(t, m, 4))
}

func TestBuildMatcher_GreaterThanOrEqualError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"gte", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThanOrEqual(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"lte", 10.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 10))
	assert.False(t, matches(t, m, 11))
}

func TestBuildMatcher_LessThanOrEqualError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"lte", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
	assert.True(t, strings.Contains(err.Error(), "lesseq") || strings.Contains(err.Error(), "lte"), "error should name 'lte': %s", err)
}

// Composite matchers

func TestBuildMatcher_All(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"all", []any{[]any{"contains", "a"}, []any{"contains", "b"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "ab"))
	assert.False(t, matches(t, m, "a"))
}

func TestBuildMatcher_Any(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"any", []any{[]any{"eq", "x"}, []any{"eq", "y"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "x"))
	assert.False(t, matches(t, m, "z"))
}

func TestBuildMatcher_Not(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"not", []any{"eq", "hello"}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "world"))
	assert.False(t, matches(t, m, "hello"))
}

func TestBuildMatcher_Nil(t *testing.T) {
	m, err := _registry.BuildMatcher("nil")
	require.NoError(t, err)
	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBuildMatcher_Present(t *testing.T) {
	m, err := _registry.BuildMatcher("present")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "value"))
}

func TestBuildMatcher_Empty(t *testing.T) {
	m, err := _registry.BuildMatcher("empty")
	require.NoError(t, err)
	assert.True(t, matches(t, m, ""))
	assert.False(t, matches(t, m, "nonempty"))
}

// Invalid JSON paths come from mock files and the admin API: they must be reported, not panic.

func TestBuildMatcher_JSONPathInvalidPathReturnsError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"jsonpath", "$[", []any{"eq", "x"}})
	require.ErrorContains(t, err, "$[")
}

func TestBuildMatcher_HasKeyInvalidPathReturnsError(t *testing.T) {
	_, err := _registry.BuildMatcher([]any{"haskey", "$["})
	require.ErrorContains(t, err, "$[")
}

// F21: a multi-matcher entry that names no matcher was a nil matcher: no error at load, then a failure or a panic
// on every request. It must be reported when the mock is built.
func TestBuildMatcher_MultiMatcherEntryMustBeAMatcher(t *testing.T) {
	for _, spec := range [][]any{
		{"all", []any{true}},
		{"any", []any{int64(5), "anything"}},
		{"both", "anything", nil},
		{"either", map[string]any{"a": int64(1)}, "anything"},
		{"xor", "anything", 1.5},
	} {
		_, err := _registry.BuildMatcher(spec)
		require.ErrorContains(t, err, "a matcher is a name or a definition", "%v", spec)
	}

	m, err := _registry.BuildMatcher([]any{"all", []any{"present", []any{"equal", "x"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "x"))
}

func TestBuildMatcher_RequestURINeedsAPathFromTheRoot(t *testing.T) {
	// A request's path starts at "/": a relative target that doesn't would never match, and no load would say why.
	_, err := _registry.BuildMatcher([]any{"requestURI", "items?page=2"})
	require.ErrorContains(t, err, `requestURI takes a URL, not "items?page=2": its path must start with /`)

	for _, ok := range []string{"/items?page=2", "http://h/items?page=2", "//a/b"} {
		_, err := _registry.BuildMatcher([]any{"requesturi", ok})
		require.NoError(t, err, ok)
	}

	for _, bad := range []string{"/a%zz", "/a#%zz"} {
		_, err := _registry.BuildMatcher([]any{"requesturi", bad})
		require.ErrorContains(t, err, fmt.Sprintf("requesturi takes a URL, not %q", bad))
	}
}

func TestBuildMatcher_HasExactly(t *testing.T) {
	// hasExactly: as many values as matchers, each matched by a matcher of its own, in any order.
	m, err := _registry.BuildMatcher([]any{"hasExactly", []any{[]any{"equal", "1"}, []any{"contains", "2"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, []any{"20", "1"}))
	assert.False(t, matches(t, m, []any{"1", "1"}))
	assert.False(t, matches(t, m, []any{"1", "20", "x"}))

	empty, err := _registry.BuildMatcher([]any{"hasexactly", []any{}})
	require.NoError(t, err)
	assert.True(t, matches(t, empty, []any{}))
	assert.False(t, matches(t, empty, []any{"a"}))
}

func TestBuildMatcher_EqualJSONOptions(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"equaljson", `{"a": [1, 2]}`, map[string]any{"ignore_array_order": true, "ignore_extra_elements": true}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, `{"a": [3, 2, 1], "b": 0}`))
	assert.False(t, matches(t, m, `{"a": [3, 2]}`))

	strict, err := _registry.BuildMatcher([]any{"eqj", `{"a": [1, 2]}`, map[string]any{"ignore_array_order": false}})
	require.NoError(t, err)
	assert.False(t, matches(t, strict, `{"a": [2, 1]}`))
}

func TestBuildMatcher_JSONSchema(t *testing.T) {
	// A mock file writes the schema as an object, or as JSON text.
	for _, schema := range []any{
		map[string]any{"type": "object", "required": []any{"name"}},
		`{"type": "object", "required": ["name"]}`,
	} {
		m, err := _registry.BuildMatcher([]any{"jsonSchema", schema})
		require.NoError(t, err)
		assert.True(t, matches(t, m, `{"name": "Rex"}`))
		assert.False(t, matches(t, m, `{"age": 3}`))
	}
}

func TestBuildMatcher_JSONSchemaDraft(t *testing.T) {
	// draft-04 writes exclusiveMaximum as a bool. YAML reads draft: 4 as a number, and draft: "2019-09" as text.
	schema := map[string]any{"maximum": int64(10), "exclusiveMaximum": true}

	for _, draft := range []any{int64(4), "4"} {
		m, err := _registry.BuildMatcher([]any{"jsonschema", schema, map[string]any{"draft": draft}})
		require.NoError(t, err)
		assert.True(t, matches(t, m, int64(9)))
		assert.False(t, matches(t, m, int64(10)))
	}

	_, err := _registry.BuildMatcher([]any{"jsonschema", schema})
	require.ErrorContains(t, err, "jsonschema takes a JSON Schema")
}

func TestBuildMatcher_EqualXML(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"equalXml", "<a><b>1</b><b>2</b></a>", map[string]any{"ignore_child_order": true}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "<a>\n  <b>2</b>\n  <b>1</b>\n</a>"))
	assert.False(t, matches(t, m, "<a><b>2</b></a>"))

	strict, err := _registry.BuildMatcher([]any{"eqx", `<p:a xmlns:p="urn:p"/>`, map[string]any{"namespaces": "strict"}})
	require.NoError(t, err)
	assert.True(t, matches(t, strict, `<p:a xmlns:p="urn:p"/>`))
	assert.False(t, matches(t, strict, `<q:a xmlns:q="urn:p"/>`))
}

func TestBuildMatcher_XPath(t *testing.T) {
	m, err := _registry.BuildMatcher([]any{"xpath", "//todo-item/text()", []any{"some", []any{"contains", "wash"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "<todo-list><todo-item>walk</todo-item><todo-item>wash</todo-item></todo-list>"))
	assert.False(t, matches(t, m, "<todo-list><todo-item>walk</todo-item></todo-list>"))

	namespaced, err := _registry.BuildMatcher([]any{
		"xpath", "/s:outer/s:inner",
		[]any{"eqx", `<y:inner xmlns:y="urn:stuff">1</y:inner>`},
		map[string]any{"namespaces": map[string]any{"s": "urn:stuff"}},
	})
	require.NoError(t, err)
	assert.True(t, matches(t, namespaced, `<x:outer xmlns:x="urn:stuff"><x:inner>1</x:inner></x:outer>`))

	_, err = _registry.BuildMatcher([]any{"xpath", "//[", "present"})
	require.ErrorContains(t, err, "xpath takes an XPath expression first: match: xpath: the expression //[ is invalid")
}

func TestBuildMatcher_DateTime(t *testing.T) {
	tests := []struct {
		spec  []any
		value string
		want  bool
	}{
		{[]any{"before", "2021-06-01T12:00:00Z"}, "2021-06-01T11:00:00Z", true},
		{[]any{"before", "2021-06-01T12:00:00Z"}, "2021-06-01T13:00:00+02:00", true},
		{[]any{"after", "Tue, 01 Jun 2021 12:00:00 GMT"}, "2021-06-01T12:00:01Z", true},
		{[]any{"after", "2021-06-01T12:00:00Z +1 days"}, "2021-06-02T12:00:00Z", false},
		{[]any{"after", "2021-06-01T12:00:00Z -1 day"}, "2021-06-01T00:00:00Z", true},
		{[]any{"before", "now +1000 years"}, "2021-06-01T12:00:00Z", true},
		{[]any{"after", "now"}, "2021-06-01T12:00:00Z", false},
		{[]any{"equalDateTime", "2021-06-01T00:00:00Z", map[string]any{"truncate_actual": "first day of month"}}, "2021-06-30T23:00:00Z", true},
		// A date and time without a zone is local: its wall clock is compared.
		{[]any{"eqdt", "2021-06-24T00:00:00", map[string]any{"actual_format": "02/01/2006"}}, "24/06/2021", true},
		{[]any{"eqdt", "2021-06-24T10:00:00"}, "2021-06-24T10:00:00+05:00", true},
	}

	for _, tc := range tests {
		m, err := _registry.BuildMatcher(tc.spec)
		require.NoError(t, err, "%v", tc.spec)
		assert.Equal(t, tc.want, matches(t, m, tc.value), "%v on %s", tc.spec, tc.value)
	}
}

func TestBuildMatcher_JSONSchemaObjectDescribesTheSameEachTime(t *testing.T) {
	// The admin API's store writes a mock as its description: one mock file must write the same file each time.
	schema := map[string]any{"type": "object", "required": []any{"a"}, "properties": map[string]any{"a": make(map[string]any), "b": make(map[string]any)}}

	var first string

	for i := range 20 {
		m, err := _registry.BuildMatcher([]any{"jsonschema", schema})
		require.NoError(t, err)

		data, err := m.(descriptor.SelfDescriptive).Describe().MarshalJSON()
		require.NoError(t, err)

		if i == 0 {
			first = string(data)
		}

		require.Equal(t, first, string(data))
	}
}

func TestBuilt_OnlyAConstructorsErrorBecomesAnError(t *testing.T) {
	// A constructor of package match panics with an error on input it can't build: that is the spec's error. Any other
	// panic is a bug, and goes on.
	errBad := errors.New("bad input")

	_, err := built(func() vlatte.Matcher { panic(errBad) })
	require.ErrorIs(t, err, errBad)

	require.PanicsWithValue(t, "not an error", func() { _, _ = built(func() vlatte.Matcher { panic("not an error") }) })

	var nilMap map[string]int
	require.Panics(t, func() { _, _ = built(func() vlatte.Matcher { nilMap["x"] = 1; return nil }) }, "a runtime error")
}

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
		{[]any{"jsonschema", "{}", "x"}, "jsonschema takes options, an object such as {draft: ...}, not a string"},
		{[]any{"eqx", "<a/>", "x"}, "eqx takes options, an object such as {ignore_child_order: ...}, not a string"},
		{[]any{"eqx", "<a/>", map[string]any{"namespaces": int64(5)}}, "eqx takes the namespaces uri, strict or none, not a number"},
		{[]any{"xpath", "/a", "present", "x"}, "xpath takes options, an object such as {namespaces: ...}, not a string"},
		{[]any{"before", "now", "x"}, "before takes options, an object such as {truncate_expected: ...}, not a string"},
		{[]any{"eqj", "{}", map[string]any{"ignore_extra_elements": "yes"}}, "eqj takes true or false for ignore_extra_elements, not a string"},
		{[]any{"eqdt", "now", map[string]any{"local_time": "yes"}}, "eqdt takes true or false for local_time, not a string"},
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

// Generators

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
