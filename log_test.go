package vlatte_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestLogVerbosity_UnknownValuesHaveNoVerbosityName(t *testing.T) {
	for _, value := range []vlatte.LogVerbosity{-1, 3} {
		require.Equal(t, "none", value.String())
	}
}
