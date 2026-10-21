package filterbuilder

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func matches(t *testing.T, m vlatte.Matcher, v any) bool {
	t.Helper()
	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
	require.NoError(t, err)
	return ok
}

// TryBuildMatcher

func TestTryBuildMatcher_NonSliceBecomesEqual(t *testing.T) {
	m, err := TryBuildMatcher("hello")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "hello"))
	assert.False(t, matches(t, m, "world"))
}

func TestTryBuildMatcher_SliceBuildsNamed(t *testing.T) {
	m, err := TryBuildMatcher([]any{"contains", "world"})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "hello world"))
	assert.False(t, matches(t, m, "hello"))
}

func TestTryBuildMatcher_NilReturnsError(t *testing.T) {
	_, err := TryBuildMatcher(nil)
	require.Error(t, err)
}

// BuildMatcher

func TestBuildMatcher_StringBuildsNoArgMatcher(t *testing.T) {
	m, err := BuildMatcher("anything")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "whatever"))
}

func TestBuildMatcher_ArrayBuildsNamed(t *testing.T) {
	m, err := BuildMatcher([]any{"eq", "test"})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "test"))
	assert.False(t, matches(t, m, "other"))
}

func TestBuildMatcher_UnknownKeyReturnsError(t *testing.T) {
	_, err := BuildMatcher([]any{"nonexistent_matcher", "arg"})
	require.Error(t, err)
}

func TestBuildMatcher_EmptyArrayReturnsError(t *testing.T) {
	_, err := BuildMatcher([]any{})
	require.Error(t, err)
}

// Split — argument order: [separator, matcher]

func TestBuildMatcher_Split(t *testing.T) {
	m, err := BuildMatcher([]any{"split", ",", []any{"contains", "b"}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "a,b,c"))
	assert.False(t, matches(t, m, "a,c,d"))
}

func TestBuildMatcher_SplitWrongArgCount(t *testing.T) {
	_, err := BuildMatcher([]any{"split", []any{","}}) // only separator, missing matcher
	require.EqualError(t, err, "split takes 2 arguments, not 1")
}

func TestBuildMatcher_SplitFirstArgNotString(t *testing.T) {
	_, err := BuildMatcher([]any{"split", int64(42), []any{"anything"}})
	require.EqualError(t, err, `split takes a separator first, not a number; write it in quotes: "42"`)
}

// Numeric matchers — error messages must use %v (not %d)

func TestBuildMatcher_GreaterThan(t *testing.T) {
	m, err := BuildMatcher([]any{"gt", 5.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 6))
	assert.False(t, matches(t, m, 4))
}

func TestBuildMatcher_GreaterThanError(t *testing.T) {
	_, err := BuildMatcher([]any{"gt", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThan(t *testing.T) {
	m, err := BuildMatcher([]any{"lt", 10.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 5))
	assert.False(t, matches(t, m, 15))
}

func TestBuildMatcher_LessThanError(t *testing.T) {
	_, err := BuildMatcher([]any{"lt", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
	assert.True(t, strings.Contains(err.Error(), "less") || strings.Contains(err.Error(), "lt"), "error should name 'lt': %s", err)
}

func TestBuildMatcher_GreaterThanOrEqual(t *testing.T) {
	m, err := BuildMatcher([]any{"gte", 5.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 5))
	assert.False(t, matches(t, m, 4))
}

func TestBuildMatcher_GreaterThanOrEqualError(t *testing.T) {
	_, err := BuildMatcher([]any{"gte", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThanOrEqual(t *testing.T) {
	m, err := BuildMatcher([]any{"lte", 10.0})
	require.NoError(t, err)
	assert.True(t, matches(t, m, 10))
	assert.False(t, matches(t, m, 11))
}

func TestBuildMatcher_LessThanOrEqualError(t *testing.T) {
	_, err := BuildMatcher([]any{"lte", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
	assert.True(t, strings.Contains(err.Error(), "lesseq") || strings.Contains(err.Error(), "lte"), "error should name 'lte': %s", err)
}

// Composite matchers

func TestBuildMatcher_All(t *testing.T) {
	m, err := BuildMatcher([]any{"all", []any{[]any{"contains", "a"}, []any{"contains", "b"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "ab"))
	assert.False(t, matches(t, m, "a"))
}

func TestBuildMatcher_Any(t *testing.T) {
	m, err := BuildMatcher([]any{"any", []any{[]any{"eq", "x"}, []any{"eq", "y"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "x"))
	assert.False(t, matches(t, m, "z"))
}

func TestBuildMatcher_Not(t *testing.T) {
	m, err := BuildMatcher([]any{"not", []any{"eq", "hello"}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "world"))
	assert.False(t, matches(t, m, "hello"))
}

func TestBuildMatcher_Nil(t *testing.T) {
	m, err := BuildMatcher("nil")
	require.NoError(t, err)
	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBuildMatcher_Present(t *testing.T) {
	m, err := BuildMatcher("present")
	require.NoError(t, err)
	assert.True(t, matches(t, m, "value"))
}

func TestBuildMatcher_Empty(t *testing.T) {
	m, err := BuildMatcher("empty")
	require.NoError(t, err)
	assert.True(t, matches(t, m, ""))
	assert.False(t, matches(t, m, "nonempty"))
}

// Invalid JSON paths come from mock files and the admin API: they must be reported, not panic.

func TestBuildMatcher_JSONPathInvalidPathReturnsError(t *testing.T) {
	_, err := BuildMatcher([]any{"jsonpath", "$[", []any{"eq", "x"}})
	require.ErrorContains(t, err, "$[")
}

func TestBuildMatcher_HasKeyInvalidPathReturnsError(t *testing.T) {
	_, err := BuildMatcher([]any{"haskey", "$["})
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
		_, err := BuildMatcher(spec)
		require.ErrorContains(t, err, "a matcher is a name or a definition", "%v", spec)
	}

	m, err := BuildMatcher([]any{"all", []any{"present", []any{"equal", "x"}}})
	require.NoError(t, err)
	assert.True(t, matches(t, m, "x"))
}

func TestBuildMatcher_RequestURINeedsAPathFromTheRoot(t *testing.T) {
	// A request's path starts at "/": a relative target that doesn't would never match, and no load would say why.
	_, err := BuildMatcher([]any{"requestURI", "items?page=2"})
	require.ErrorContains(t, err, `requestURI takes a URL, not "items?page=2": its path must start with /`)

	for _, ok := range []string{"/items?page=2", "http://h/items?page=2", "//a/b"} {
		_, err := BuildMatcher([]any{"requesturi", ok})
		require.NoError(t, err, ok)
	}

	for _, bad := range []string{"/a%zz", "/a#%zz"} {
		_, err := BuildMatcher([]any{"requesturi", bad})
		require.ErrorContains(t, err, fmt.Sprintf("requesturi takes a URL, not %q", bad))
	}
}
