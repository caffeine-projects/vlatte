package fault_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

// call is a call a flow gets from its caller.
type call struct {
	name string
	do   func(flow *vlatte.Flow) error
}

// _calls are the first calls a fault can get: the first bytes of the answer, a flush, or the end of an answer with no
// bytes.
var _calls = []call{
	{name: "write", do: func(flow *vlatte.Flow) error { _, err := flow.Write([]byte("HTTP/1.1 200 OK\r\n\r\n")); return err }},
	{name: "flush", do: (*vlatte.Flow).Flush},
	{name: "close", do: (*vlatte.Flow).Close},
}

func TestEmptyResponse_SendsNothingAndAborts(t *testing.T) {
	// The client gets no byte of the answer, not even a status line, and the server ends the connection.
	for _, c := range _calls {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.EmptyResponse()})

			require.ErrorIs(t, c.do(flow), vlatte.ErrAbort)

			cause := context.Cause(flow.Context())
			require.ErrorIs(t, cause, vlatte.ErrAbort)
			require.NotErrorIs(t, cause, vlatte.ErrReset)
			require.Empty(t, out.String())
		})
	}
}
