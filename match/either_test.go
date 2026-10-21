package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestEither(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []vlatte.Matcher
		expected bool
	}{
		{"left true", []vlatte.Matcher{StrictEqual("test"), Contain("qa")}, true},
		{"right true", []vlatte.Matcher{StrictEqual("qa"), Contain("tes")}, true},
		{"both true", []vlatte.Matcher{StrictEqual("test"), Contain("te")}, true},
		{"both false", []vlatte.Matcher{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Either(tc.matchers[0], tc.matchers[1]).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestEitherLeftErr(t *testing.T) {
	ok, err := Either(
		Function(func(_ any) (bool, error) {
			return false, errors.New("fail")
		}),
		Contain("qa")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}

func TestEitherRightErr(t *testing.T) {
	ok, err := Either(
		Contain("qa"),
		Function(func(_ any) (bool, error) {
			return false, errors.New("fail")
		})).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}
