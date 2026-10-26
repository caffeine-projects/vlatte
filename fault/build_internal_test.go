package fault

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestBuilt_ABugStillPanics(t *testing.T) {
	// Only the messages constructors panic with on input they refuse become errors: a bug, such as an index out of
	// range, must not pass for a spec that is wrong.
	require.PanicsWithError(t, "runtime error: index out of range [1] with length 0", func() {
		_, _ = built(func() vlatte.Step {
			var steps []vlatte.Step
			return steps[1]
		})
	})
}
