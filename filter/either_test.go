package filter

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEither(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []Filter
		expected bool
	}{
		{"left true", []Filter{StrictEqual("test"), Contain("qa")}, true},
		{"right true", []Filter{StrictEqual("qa"), Contain("tes")}, true},
		{"both true", []Filter{StrictEqual("test"), Contain("te")}, true},
		{"both false", []Filter{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := Either(tc.matchers[0], tc.matchers[1]).Match("test")
			require.NoError(t, err)
			require.Equal(t, tc.expected, result.Pass)
		})
	}
}

func TestEitherLeftErr(t *testing.T) {
	result, err := Either(
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		}),
		Contain("qa")).Match("test")

	require.Error(t, err)
	require.False(t, result.Pass)
}

func TestEitherRightErr(t *testing.T) {
	result, err := Either(
		Contain("qa"),
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		})).Match("test")

	require.Error(t, err)
	require.False(t, result.Pass)
}
