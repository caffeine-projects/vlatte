package fault

import (
	"context"
	"fmt"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Step  = limitData{}
	_ vlatte.Stage = (*limitDataStage)(nil)
)

// LimitData returns a step that passes the first n bytes of the answer on, flushed, then aborts the flow, so the
// connection closes, to test a client against an answer cut short. An answer of fewer than n bytes passes whole, and
// ends as it would; a longer one, or one of n bytes, is cut once its n-th byte went out. A limit of 0 cuts the answer
// at its first byte.
//
// With [github.com/caffeine-projects/vlatte/mockhttp.Builder.Step], n counts the bytes of the body. With
// [github.com/caffeine-projects/vlatte/mockhttp.Builder.WireStep], it counts every byte of the answer, its status
// line, headers and framing included. It panics when n is negative.
func LimitData(n int) vlatte.Step {
	if n < 0 {
		panic(fmt.Sprintf("fault: limit data can't be %d bytes", n))
	}

	return limitData{n: n}
}

type limitData struct{ n int }

func (l limitData) Open(flow *vlatte.Flow, next vlatte.Stage) vlatte.Stage {
	return &limitDataStage{flow: flow, left: l.n, next: next}
}

// limitDataStage passes on what is left of its limit, then aborts its flow.
type limitDataStage struct {
	flow *vlatte.Flow
	left int
	next vlatte.Stage
}

func (s *limitDataStage) Write(b []byte) (int, error) {
	if len(b) < s.left || len(b) == 0 {
		s.left -= len(b)
		return s.next.Write(b)
	}

	n := s.left
	s.left = 0

	if n > 0 {
		if _, err := s.next.Write(b[:n]); err != nil {
			return 0, err
		}

		if err := s.next.Flush(); err != nil {
			return n, err
		}
	}

	s.flow.Abort(nil)

	return n, context.Cause(s.flow.Context())
}

func (s *limitDataStage) Flush() error { return s.next.Flush() }

func (s *limitDataStage) Close() error { return s.next.Close() }
