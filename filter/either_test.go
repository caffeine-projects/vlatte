package filter

import (
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/require"
)

func TestEither(t *testing.T) {
	testCases := []struct {
		name     string
		matchers []vlatte.Filter
		expected bool
	}{
		{"left true", []vlatte.Filter{StrictEqual("test"), Contain("qa")}, true},
		{"right true", []vlatte.Filter{StrictEqual("qa"), Contain("tes")}, true},
		{"both true", []vlatte.Filter{StrictEqual("test"), Contain("te")}, true},
		{"both false", []vlatte.Filter{StrictEqual("qa"), Contain("dev")}, false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ok, err := Either(tc.matchers[0], tc.matchers[1]).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestEitherLeftErr(t *testing.T) {
	ok, err := Either(
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		}),
		Contain("qa")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}

func TestEitherRightErr(t *testing.T) {
	ok, err := Either(
		Contain("qa"),
		Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		})).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))

	require.Error(t, err)
	require.False(t, ok)
}
