package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestNot(t *testing.T) {
	tcs := []struct {
		name     string
		matcher  vlatte.Matcher
		expected bool
	}{
		{"is not equal", StrictEqual("dev"), true},
		{"is equal", StrictEqual("test"), false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Not(tc.matcher).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestNot_KeepsWhyItsMatcherDidNotMatch(t *testing.T) {
	// A mock that matches through a not, such as one that refuses a request breaking a rule, can tell why from the
	// explanation: what its matcher said.
	exp := vlatte.NewExplanation()

	ok, err := Not(StrictEqual("admin")).Match(vlatte.NewMatcherArg(t.Context(), exp, "user"))

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"StrictEq(admin) Got: user"}, exp.Messages())
}
