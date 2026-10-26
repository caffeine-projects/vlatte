package fault_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestConnectionResetByPeer_SendsNothingAndResets(t *testing.T) {
	// A reset is an error a client must handle apart from a connection that ends: the flow says which.
	for _, c := range _calls {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.ConnectionResetByPeer()})

			require.ErrorIs(t, c.do(flow), vlatte.ErrReset)

			cause := context.Cause(flow.Context())
			require.ErrorIs(t, cause, vlatte.ErrAbort)
			require.ErrorIs(t, cause, vlatte.ErrReset)
			require.Empty(t, out.String())
		})
	}
}
