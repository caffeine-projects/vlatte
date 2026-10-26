package fault_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
)

func TestLimitData_CutsTheAnswerAtTheLimit(t *testing.T) {
	// The client gets the first bytes, then the end of the connection, wherever the limit falls in the writes.
	tests := []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{name: "within a write", limit: 3, writes: []string{"abcdef"}, want: "abc"},
		{name: "in a later write", limit: 5, writes: []string{"abc", "defg"}, want: "abcde"},
		{name: "at the end of a write", limit: 3, writes: []string{"abc", "def"}, want: "abc"},
		{name: "no byte", limit: 0, writes: []string{"abc"}, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.LimitData(tc.limit)})

			var err error
			for _, w := range tc.writes {
				if _, err = flow.Write([]byte(w)); err != nil {
					break
				}
			}

			require.ErrorIs(t, err, vlatte.ErrAbort)
			require.ErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
			require.Equal(t, tc.want, out.String())

			// What the reply writes after the cut goes nowhere.
			_, err = flow.Write([]byte("more"))
			require.ErrorIs(t, err, vlatte.ErrAbort)
			require.Equal(t, tc.want, out.String())
		})
	}
}

func TestLimitData_TellsTheWriterHowMuchWentOut(t *testing.T) {
	// The write that meets the limit took only what fit, and the rest is flushed so the client gets it before the cut.
	sink := &sliceSink{}
	flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.LimitData(4)})

	n, err := flow.Write([]byte("ab"))
	require.NoError(t, err)
	require.Equal(t, 2, n)

	n, err = flow.Write([]byte("cdef"))
	require.ErrorIs(t, err, vlatte.ErrAbort)
	require.Equal(t, 2, n)
	require.Equal(t, []string{"ab", "cd"}, sink.writes)
}

func TestLimitData_AShorterAnswerEndsAsItWould(t *testing.T) {
	// An answer under the limit isn't cut: the limit tests a long answer only.
	tests := []struct {
		name   string
		limit  int
		writes []string
	}{
		{name: "a longer limit", limit: 10, writes: []string{"abc", "def"}},
		{name: "no byte at a limit of 0", limit: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			flow := vlatte.NewFlow(t.Context(), &vlatte.Sink{W: &out}, []vlatte.Step{fault.LimitData(tc.limit)})

			for _, w := range tc.writes {
				_, err := flow.Write([]byte(w))
				require.NoError(t, err)
			}

			require.NoError(t, flow.Flush())
			require.NoError(t, flow.Close())
			require.NotErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
			require.Equal(t, strings.Join(tc.writes, ""), out.String())
		})
	}
}

func TestLimitData_AClientThatWentAwayEndsIt(t *testing.T) {
	// A write the client can't get fails the answer with its own error, not a cut.
	errGone := errors.New("client: gone")

	for name, sink := range map[string]failingSink{
		"a write at the limit":   {err: errGone, writes: true},
		"the flush at the limit": {err: errGone, flushes: true},
	} {
		t.Run(name, func(t *testing.T) {
			flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{fault.LimitData(2)})

			_, err := flow.Write([]byte("abc"))
			require.ErrorIs(t, err, errGone)
			require.NotErrorIs(t, context.Cause(flow.Context()), vlatte.ErrAbort)
		})
	}
}

func TestLimitData_RefusesANegativeLimit(t *testing.T) {
	// A limit under no byte is a mistake of the mock: it fails as the mock is built.
	require.PanicsWithValue(t, "fault: limit data can't be -1 bytes", func() { fault.LimitData(-1) })
}
