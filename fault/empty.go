package fault

import (
	"context"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ vlatte.Step                = emptyResponse{}
	_ descriptor.SelfDescriptive = emptyResponse{}
	_ vlatte.Stage               = (*emptyStage)(nil)
)

// EmptyResponse returns a wire step that sends nothing: at the first byte of the answer, or at its end, it aborts the
// flow, so the connection closes before a status line. The client reads the end of the connection where it waits for
// an answer.
func EmptyResponse() vlatte.Step {
	return emptyResponse{}
}

type emptyResponse struct{}

// Describe implements [descriptor.SelfDescriptive].
func (emptyResponse) Describe() descriptor.Descriptor {
	return descriptor.WithKeyValuePairs("empty_response", "type", "empty_response")
}

func (emptyResponse) Open(flow *vlatte.Flow, _ vlatte.Stage) vlatte.Stage {
	return &emptyStage{flow: flow}
}

// emptyStage aborts its flow at its first call.
type emptyStage struct{ flow *vlatte.Flow }

func (s *emptyStage) Write([]byte) (int, error) { return 0, s.abort() }

func (s *emptyStage) Flush() error { return s.abort() }

func (s *emptyStage) Close() error { return s.abort() }

func (s *emptyStage) abort() error {
	s.flow.Abort(nil)
	return context.Cause(s.flow.Context())
}
