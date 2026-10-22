package fault

import (
	"context"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step  = resetByPeer{}
	_ vlatte.Stage = (*resetStage)(nil)
)

// ConnectionResetByPeer returns a wire step that resets the connection: at the first byte of the answer, or at its
// end, it aborts the flow with [vlatte.ErrReset], so the connection closes with a TCP reset before a status line. The
// client reads a reset, such as syscall.ECONNRESET, not an end.
func ConnectionResetByPeer() vlatte.Step {
	return resetByPeer{}
}

type resetByPeer struct{}

func (resetByPeer) Open(flow *vlatte.Flow, _ vlatte.Stage) vlatte.Stage {
	return &resetStage{flow: flow}
}

// resetStage aborts its flow with a reset at its first call.
type resetStage struct{ flow *vlatte.Flow }

func (s *resetStage) Write([]byte) (int, error) { return 0, s.reset() }

func (s *resetStage) Flush() error { return s.reset() }

func (s *resetStage) Close() error { return s.reset() }

func (s *resetStage) reset() error {
	s.flow.Abort(vlatte.ErrReset)
	return context.Cause(s.flow.Context())
}
