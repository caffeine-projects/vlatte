package filterbuilder

import (
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func match(t *testing.T, m vlatte.Filter, v any) bool {
	t.Helper()
	ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), v))
	require.NoError(t, err)
	return ok
}

// TryBuildMatcher

func TestTryBuildMatcher_NonSliceBecomesEqual(t *testing.T) {
	m, err := TryBuildMatcher("hello")
	require.NoError(t, err)
	assert.True(t, match(t, m, "hello"))
	assert.False(t, match(t, m, "world"))
}

func TestTryBuildMatcher_SliceBuildsNamed(t *testing.T) {
	m, err := TryBuildMatcher([]any{"contains", "world"})
	require.NoError(t, err)
	assert.True(t, match(t, m, "hello world"))
	assert.False(t, match(t, m, "hello"))
}

func TestTryBuildMatcher_NilReturnsError(t *testing.T) {
	_, err := TryBuildMatcher(nil)
	require.Error(t, err)
}

// BuildMatcher

func TestBuildMatcher_StringBuildsNoArgMatcher(t *testing.T) {
	m, err := BuildMatcher("anything")
	require.NoError(t, err)
	assert.True(t, match(t, m, "whatever"))
}

func TestBuildMatcher_ArrayBuildsNamed(t *testing.T) {
	m, err := BuildMatcher([]any{"eq", "test"})
	require.NoError(t, err)
	assert.True(t, match(t, m, "test"))
	assert.False(t, match(t, m, "other"))
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
	assert.True(t, match(t, m, "a,b,c"))
	assert.False(t, match(t, m, "a,c,d"))
}

func TestBuildMatcher_SplitWrongArgCount(t *testing.T) {
	_, err := BuildMatcher([]any{"split", []any{","}}) // only separator, missing matcher
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "1: Separator, 2: Matcher"), "got: %s", err)
}

func TestBuildMatcher_SplitFirstArgNotString(t *testing.T) {
	_, err := BuildMatcher([]any{"split", 42, []any{"anything"}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "first parameter"))
}

// Numeric matchers — error messages must use %v (not %d)

func TestBuildMatcher_GreaterThan(t *testing.T) {
	m, err := BuildMatcher([]any{"gt", 5.0})
	require.NoError(t, err)
	assert.True(t, match(t, m, 6))
	assert.False(t, match(t, m, 4))
}

func TestBuildMatcher_GreaterThanError(t *testing.T) {
	_, err := BuildMatcher([]any{"gt", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThan(t *testing.T) {
	m, err := BuildMatcher([]any{"lt", 10.0})
	require.NoError(t, err)
	assert.True(t, match(t, m, 5))
	assert.False(t, match(t, m, 15))
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
	assert.True(t, match(t, m, 5))
	assert.False(t, match(t, m, 4))
}

func TestBuildMatcher_GreaterThanOrEqualError(t *testing.T) {
	_, err := BuildMatcher([]any{"gte", "not-a-number"})
	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), "%!d"), "error should not contain %%d formatting artifact: %s", err)
}

func TestBuildMatcher_LessThanOrEqual(t *testing.T) {
	m, err := BuildMatcher([]any{"lte", 10.0})
	require.NoError(t, err)
	assert.True(t, match(t, m, 10))
	assert.False(t, match(t, m, 11))
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
	assert.True(t, match(t, m, "ab"))
	assert.False(t, match(t, m, "a"))
}

func TestBuildMatcher_Any(t *testing.T) {
	m, err := BuildMatcher([]any{"any", []any{[]any{"eq", "x"}, []any{"eq", "y"}}})
	require.NoError(t, err)
	assert.True(t, match(t, m, "x"))
	assert.False(t, match(t, m, "z"))
}

func TestBuildMatcher_Not(t *testing.T) {
	m, err := BuildMatcher([]any{"not", []any{"eq", "hello"}})
	require.NoError(t, err)
	assert.True(t, match(t, m, "world"))
	assert.False(t, match(t, m, "hello"))
}

func TestBuildMatcher_Nil(t *testing.T) {
	m, err := BuildMatcher("nil")
	require.NoError(t, err)
	ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestBuildMatcher_Present(t *testing.T) {
	m, err := BuildMatcher("present")
	require.NoError(t, err)
	assert.True(t, match(t, m, "value"))
}

func TestBuildMatcher_Empty(t *testing.T) {
	m, err := BuildMatcher("empty")
	require.NoError(t, err)
	assert.True(t, match(t, m, ""))
	assert.False(t, match(t, m, "nonempty"))
}
