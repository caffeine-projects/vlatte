package sse

import (
	"context"
	"io"
	"slices"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/delay"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

var _ io.ReadCloser = (*Stream)(nil)

// Stream reads an event stream as it is sent: each Read returns the next event, or a heartbeat, once its wait is
// over. It ends with io.EOF after the last event, or, when the stream keeps open, once its context ends. A context
// that ends, as when the client leaves, ends it with io.EOF too: the client got every event it read.
type Stream struct {
	ctx        context.Context
	frames     []Frame
	next       int
	pending    []byte
	eventDelay vlatte.Delay
	heartbeat  time.Duration
	keepOpen   bool
	waiting    bool          // the wait before the next frame started
	left       time.Duration // what is left of that wait
	done       bool
}

// Stream renders the events with data and returns the stream that sends them, until ctx ends. lastEventID is the
// Last-Event-ID of the request, "" when it has none: a resuming stream starts after the event with that id, or at
// the first when none has it.
func (e *Encoder) Stream(ctx context.Context, data any, lastEventID string) (*Stream, error) {
	frames, err := e.Encode(data)
	if err != nil {
		return nil, err
	}

	if e.resume && lastEventID != "" {
		if i := slices.IndexFunc(frames, func(f Frame) bool { return f.HasID && f.ID == lastEventID }); i >= 0 {
			frames = frames[i+1:]
		}
	}

	return &Stream{
		ctx:        ctx,
		frames:     frames,
		pending:    e.head,
		eventDelay: e.eventDelay,
		heartbeat:  e.heartbeat,
		keepOpen:   e.keepOpen,
	}, nil
}

// Read implements io.Reader. It blocks until the next event or heartbeat is due. A delay evaluation failure is
// returned to the reader; cancellation retains the stream's EOF behavior.
func (s *Stream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		if s.done {
			return 0, io.EOF
		}

		if err := s.advance(); err != nil {
			s.done = true
			if s.ctx.Err() == nil {
				return 0, err
			}
		}
	}

	n := copy(p, s.pending)
	s.pending = s.pending[n:]

	return n, nil
}

// Close ends the stream: a Read after it returns io.EOF.
func (s *Stream) Close() error {
	s.done = true
	s.pending = nil

	return nil
}

// advance waits for what comes next and makes it pending: the next event, a heartbeat while a wait goes past the
// heartbeat, or nothing once the stream ends. It returns the error of the context that ended the wait.
func (s *Stream) advance() error {
	if s.next == len(s.frames) {
		if !s.keepOpen {
			s.done = true
			return nil
		}

		if s.heartbeat == 0 {
			<-s.ctx.Done()
			return s.ctx.Err()
		}

		if err := httpx.Wait(s.ctx, s.heartbeat); err != nil {
			return err
		}

		s.pending = _heartbeat

		return nil
	}

	f := &s.frames[s.next]

	if !s.waiting {
		s.waiting = true
		s.left = f.Delay

		if !f.HasDelay && s.eventDelay != nil {
			var err error
			s.left, err = delay.Next(s.ctx, s.eventDelay)
			if err != nil {
				return err
			}
		}
	}

	if s.heartbeat > 0 && s.left > s.heartbeat {
		if err := httpx.Wait(s.ctx, s.heartbeat); err != nil {
			return err
		}

		s.left -= s.heartbeat
		s.pending = _heartbeat

		return nil
	}

	if s.left > 0 {
		if err := httpx.Wait(s.ctx, s.left); err != nil {
			return err
		}
	} else if err := s.ctx.Err(); err != nil {
		return err
	}

	s.waiting = false
	s.next++
	s.pending = f.Bytes

	return nil
}
