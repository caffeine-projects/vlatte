package matchbuild

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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
	// WireMock's hasExactly: as many values as matchers, each matched by a matcher of its own, in any order.
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
		[]any{"equal", "<x:inner>1</x:inner>"},
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
