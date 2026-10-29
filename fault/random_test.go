package fault_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestRandomDataThenClose_SendsGarbageInPlaceOfTheAnswer(t *testing.T) {
	// The client reads bytes where it waits for a status line, none of the answer's, then the end of the connection.
	for _, c := range _calls {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Pipe{fault.RandomDataThenClose()})

			require.ErrorIs(t, c.do(flow), vlatte.ErrAbort)
			require.ErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)

			require.Equal(t, fault.RandomDataSize, out.Len())
			require.False(t, bytes.HasPrefix(out.Bytes(), []byte("HTTP/")))

			// The answer that comes after doesn't go out, nor garbage again as the cut connection ends.
			_, err := flow.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			require.ErrorIs(t, err, vlatte.ErrAbort)
			_ = flow.Close()
			require.Equal(t, fault.RandomDataSize, out.Len())
		})
	}
}

func TestRandomDataThenClose_SendsOtherBytesEachTime(t *testing.T) {
	// Garbage a client could learn to expect would test nothing.
	answers := make([][]byte, 2)
	for i := range answers {
		var out bytes.Buffer
		_ = vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Pipe{fault.RandomDataThenClose()}).Close()
		answers[i] = out.Bytes()
	}

	require.NotEqual(t, answers[0], answers[1])
}
