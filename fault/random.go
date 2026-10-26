package fault

import (
	"context"
	"crypto/rand"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Step                = randomData{}
	_ descriptor.SelfDescriptive = randomData{}
	_ vlatte.Stage               = (*randomStage)(nil)
)

// RandomDataSize is how many random bytes [RandomDataThenClose] sends.
const RandomDataSize = 1 << 10

// RandomDataThenClose returns a wire step that sends [RandomDataSize] random bytes in place of the answer, then aborts
// the flow, so the connection closes: at the first byte of the answer, or at its end. The client reads garbage where
// it waits for a status line.
func RandomDataThenClose() vlatte.Step {
	return randomData{}
}

type randomData struct{}

// Describe implements [descriptor.SelfDescriptive].
func (randomData) Describe() descriptor.Descriptor {
	return descriptor.WithKeyValuePairs("random_data_then_close", "type", "random_data_then_close")
}

func (randomData) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &randomStage{flow: flow, next: next}
}

// randomStage sends random bytes in place of what it gets, at its first call, then aborts its flow.
type randomStage struct {
	flow *vlatte.Flow
	next vlatte.Stage
}

func (s *randomStage) Write([]byte) (int, error) { return 0, s.garbage() }

func (s *randomStage) Flush() error { return s.garbage() }

func (s *randomStage) Close() error { return s.garbage() }

func (s *randomStage) garbage() error {
	b := make([]byte, RandomDataSize)
	_, _ = rand.Read(b)

	if _, err := s.next.Write(b); err == nil {
		_ = s.next.Flush()
	}

	s.flow.Abort(nil)

	return context.Cause(s.flow.Context())
}
