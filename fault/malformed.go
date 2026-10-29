package fault

import (
	"bytes"
	"context"
	"slices"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step            = malformedChunk{}
	_ vlatte.SelfDescriptive = malformedChunk{}
	_ vlatte.Stage           = (*malformedStage)(nil)
)

// _headEnd ends the status line and headers of an answer.
var _headEnd = []byte("\r\n\r\n")

// _malformedChunk is what MalformedResponseChunk sends in place of the first chunk: its size isn't a hex number.
var _malformedChunk = []byte("not a chunk\r\nmalformed\r\n")

// MalformedResponseChunk returns a wire step that breaks the body of a chunked answer: it passes the status line and
// the headers on, with the mock's own status, then sends garbage in place of the first chunk, and aborts the flow, so
// the connection closes. The client reads a valid head, then fails to read the body. It breaks the answer after the
// first head, an informational one, such as 103 Early Hints, included. The body of an HTTP/1.0 answer isn't
// chunked: the client reads the garbage as the body.
func MalformedResponseChunk() vlatte.Step {
	return malformedChunk{}
}

type malformedChunk struct{}

// Describe implements [vlatte.SelfDescriptive].
func (malformedChunk) Describe() vlatte.Descriptor {
	return vlatte.DescribeKeyValuePairs("malformed_response_chunk", "type", "malformed_response_chunk")
}

func (malformedChunk) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &malformedStage{flow: flow, next: next}
}

// malformedStage passes what it gets on up to the end of the head, then sends garbage and aborts its flow.
type malformedStage struct {
	flow *vlatte.Flow
	next vlatte.Stage
	tail []byte // the last bytes of the head so far, where its end may start
}

func (s *malformedStage) Write(b []byte) (int, error) {
	seen := slices.Concat(s.tail, b)

	i := bytes.Index(seen, _headEnd)
	if i < 0 {
		s.tail = slices.Clone(seen[max(0, len(seen)-len(_headEnd)+1):])

		if _, err := s.next.Write(b); err != nil {
			return 0, err
		}

		return len(b), nil
	}

	if _, err := s.next.Write(b[:i+len(_headEnd)-len(s.tail)]); err != nil {
		return 0, err
	}

	if _, err := s.next.Write(_malformedChunk); err == nil {
		_ = s.next.Flush()
	}

	s.flow.Abort(nil)

	return 0, context.Cause(s.flow.Context())
}

func (s *malformedStage) Flush() error { return s.next.Flush() }

func (s *malformedStage) Close() error { return s.next.Close() }
