package filter

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBothMatcher(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []Filter
		expected bool
	}{
		{"left true", []Filter{StrictEqual("test"), Contain("qa")}, false},
		{"right true", []Filter{StrictEqual("qa"), Contain("tes")}, false},
		{"both true", []Filter{StrictEqual("test"), Contain("te")}, true},
		{"both false", []Filter{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := Both(tc.matchers[0], tc.matchers[1]).Match("test")
			require.Nil(t, err)
			require.Equal(t, tc.expected, result.Pass)
		})
	}
}

func TestBothMatcherErr(t *testing.T) {
	result, err := Both(
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		}),
		Contain("qa")).Match("test")

	require.Error(t, err)
	require.False(t, result.Pass)
}
