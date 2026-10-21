package match

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestBothMatcher(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []vlatte.Matcher
		expected bool
	}{
		{"left true", []vlatte.Matcher{StrictEqual("test"), Contain("qa")}, false},
		{"right true", []vlatte.Matcher{StrictEqual("qa"), Contain("tes")}, false},
		{"both true", []vlatte.Matcher{StrictEqual("test"), Contain("te")}, true},
		{"both false", []vlatte.Matcher{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Both(tc.matchers[0], tc.matchers[1]).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
			require.Nil(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestBothMatcherErr(t *testing.T) {
	ok, err := Both(
		Function(func(_ any) (bool, error) {
			return false, errors.New("fail")
		}),
		Contain("qa")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}
