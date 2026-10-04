package filter

import (
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestBothMatcher(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []vlatte.Filter
		expected bool
	}{
		{"left true", []vlatte.Filter{StrictEqual("test"), Contain("qa")}, false},
		{"right true", []vlatte.Filter{StrictEqual("qa"), Contain("tes")}, false},
		{"both true", []vlatte.Filter{StrictEqual("test"), Contain("te")}, true},
		{"both false", []vlatte.Filter{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Both(tc.matchers[0], tc.matchers[1]).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
			require.Nil(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestBothMatcherErr(t *testing.T) {
	ok, err := Both(
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		}),
		Contain("qa")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}
