package matchbuild

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
)

var errNotALength = errors.New("takes a length")

// longerThan builds [longer-than, n]: text longer than n bytes. Like most custom matchers, it doesn't describe
// itself.
func longerThan(_ *Registry, args []any) (vlatte.Matcher, error) {
	if len(args) != 1 {
		return nil, errNotALength
	}

	n, ok := args[0].(int64)
	if !ok {
		return nil, errNotALength
	}

	return match.Function(func(v any) (bool, error) {
		s, isText := v.(string)
		return isText && int64(len(s)) > n, nil
	}), nil
}

func TestRegistry_BuildsACustomMatcherByNameInAnyCase(t *testing.T) {
	r := MustNew(WithMatcher("longer-than", longerThan))

	m, err := r.BuildMatcher([]any{"Longer-Than", int64(3)})
	require.NoError(t, err)
	require.True(t, matches(t, m, "abcd"))
	require.False(t, matches(t, m, "abc"))
}

func TestRegistry_ACustomMatcherKeepsItsName(t *testing.T) {
	// A custom matcher describes itself as its spec, but logs show the name it gives itself.
	m, err := MustNew(WithMatcher("longer-than", longerThan)).BuildMatcher([]any{"longer-than", int64(3)})
	require.NoError(t, err)
	require.Equal(t, "function", m.Name())
}

func TestRegistry_CustomMatcherTakesNestedSpecs(t *testing.T) {
	// A custom matcher builds its arguments with the registry building it, so it can nest other custom matchers.
	r := MustNew(
		WithMatcher("longer-than", longerThan),
		WithMatcher("trimmed", func(r *Registry, args []any) (vlatte.Matcher, error) {
			inner, err := r.BuildMatcher(args[0])
			if err != nil {
				return nil, err
			}

			return match.Trim(inner), nil
		}),
	)

	m, err := r.BuildMatcher([]any{"trimmed", []any{"longer-than", int64(3)}})
	require.NoError(t, err)
	require.True(t, matches(t, m, " abcd "))
	require.False(t, matches(t, m, " abc  "))
}

func TestRegistry_CustomMatcherInsideABuiltInOne(t *testing.T) {
	r := MustNew(WithMatcher("longer-than", longerThan))

	m, err := r.BuildMatcher([]any{"not", []any{"longer-than", int64(3)}})
	require.NoError(t, err)
	require.True(t, matches(t, m, "abc"))
	require.False(t, matches(t, m, "abcd"))
}

func TestRegistry_CustomMatcherLoadsBackFromItsDescription(t *testing.T) {
	// The admin store keeps a mock as its description and refuses one that doesn't load back. A matcher that doesn't
	// describe itself became ["anything"] inside not, so [not, [longer-than, 3]] would load as a matcher of nothing.
	r := MustNew(WithMatcher("longer-than", longerThan))

	m, err := r.BuildMatcher([]any{"not", []any{"longer-than", int64(3)}})
	require.NoError(t, err)

	desc, ok := vlatte.IsSelfDescriptive(m)
	require.True(t, ok)

	data, err := desc.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["not", ["longer-than", 3]]`, string(data))

	spec, err := value.DecodeJSON(data)
	require.NoError(t, err)

	loaded, err := r.BuildMatcher(spec)
	require.NoError(t, err)
	require.True(t, matches(t, loaded, "abc"))
	require.False(t, matches(t, loaded, "abcd"))
}

func TestRegistry_KeepsTheDescriptionOfACustomMatcherThatHasOne(t *testing.T) {
	r := MustNew(WithMatcher("short", func(*Registry, []any) (vlatte.Matcher, error) {
		return match.Len(1), nil
	}))

	m, err := r.BuildMatcher("short")
	require.NoError(t, err)
	require.Equal(t, match.Len(1), m)
}

func TestRegistry_ErrorOfACustomBuilderNamesTheMatcher(t *testing.T) {
	r := MustNew(WithMatcher("longer-than", longerThan))

	_, err := r.BuildMatcher([]any{"longer-than", "three"})
	require.ErrorIs(t, err, errNotALength)
	require.EqualError(t, err, "longer-than: takes a length")
}

func TestRegistry_RefusesACustomMatcherBuiltAsNil(t *testing.T) {
	r := MustNew(WithMatcher("nothing", func(*Registry, []any) (vlatte.Matcher, error) { return nil, nil }))

	_, err := r.BuildMatcher("nothing")
	require.EqualError(t, err, "nothing built no matcher")
}

func TestRegistry_ChecksCustomArgumentsAreFinite(t *testing.T) {
	// YAML can write .nan, which no request value equals.
	r := MustNew(WithMatcher("longer-than", longerThan))

	_, err := r.BuildMatcher([]any{"longer-than", []any{float64(0) / zero()}})
	require.EqualError(t, err, "longer-than takes finite numbers, not NaN")
}

func zero() float64 { return 0 }

func TestRegistry_CustomMatchersBelongToTheirRegistry(t *testing.T) {
	// Each server has a registry of its own: a matcher given to one is unknown to another.
	MustNew(WithMatcher("longer-than", longerThan))

	_, err := MustNew().BuildMatcher([]any{"longer-than", int64(3)})
	require.EqualError(t, err, `unknown matcher "longer-than"`)
}

func TestNew_RefusesNamesThatAreTakenOrEmpty(t *testing.T) {
	_, err := New(
		WithMatcher("equal", longerThan),
		WithMatcher("EQ", longerThan),
		WithMatcher("longer-than", longerThan),
		WithMatcher("Longer-Than", longerThan),
		WithMatcher("", longerThan),
	)
	require.ErrorIs(t, err, ErrNameTaken)
	require.ErrorIs(t, err, ErrNameEmpty)
	require.EqualError(t, err, `matchbuild: matcher name is taken: "equal"
matchbuild: matcher name is taken: "EQ"
matchbuild: matcher name is taken: "Longer-Than"
matchbuild: matcher name is empty`)
}

func TestNew_RefusesAMatcherWithoutABuildFunction(t *testing.T) {
	_, err := New(WithMatcher("nothing", nil))
	require.EqualError(t, err, `matchbuild: matcher "nothing" has no build function`)
}

func TestMustNew_PanicsOnABadName(t *testing.T) {
	require.PanicsWithError(t, `matchbuild: matcher name is taken: "equal"`, func() {
		MustNew(WithMatcher("equal", longerThan))
	})
}
