package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
)

var errReplySeqEmpty = errors.New("reply_sequence: no replies: give Seq one at least")

var (
	_ Reply     = (*SequentialReply)(nil)
	_ Replier   = (*sequenceReplier)(nil)
	_ io.Closer = (*sequenceReplier)(nil)
)

// SequentialReply answers the calls of a mock with its replies, one per call, in order, then with the one set with
// OnSequenceEnded. A call of any type takes the next reply, a stream as a whole. Each mock built with it runs the
// sequence on its own, from the first reply. A call that comes once the sequence is over, without OnSequenceEnded,
// fails: the client gets Internal with the vlatte-error trailer.
type SequentialReply struct {
	replyAfterSequenceEnded Reply
	replies                 []Reply
}

// Seq creates a new SequentialReply.
func Seq(reply ...Reply) *SequentialReply {
	return &SequentialReply{replies: reply}
}

// OnSequenceEnded sets the reply of the calls that come once the sequence is over.
func (r *SequentialReply) OnSequenceEnded(reply Reply) *SequentialReply {
	r.replyAfterSequenceEnded = reply
	return r
}

// Add adds replies to the sequence.
func (r *SequentialReply) Add(reply ...Reply) *SequentialReply {
	r.replies = append(r.replies, reply...)
	return r
}

// NewReplier implements Reply. It makes the replier of each reply of the sequence, which checks itself against the
// methods, so each mock has replies and a place in the sequence of its own.
func (r *SequentialReply) NewReplier(app *Server, methods []protoreflect.MethodDescriptor) (Replier, error) {
	if len(r.replies) == 0 {
		return nil, errReplySeqEmpty
	}

	s := &sequenceReplier{replies: make([]Replier, len(r.replies))}
	for i, f := range r.replies {
		if f == nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: response %d is nil", i)
		}

		reply, err := newReplier(f, app, methods)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: response %d: %w", i, err)
		}

		s.replies[i] = reply
		s.own(reply)
	}

	if r.replyAfterSequenceEnded != nil {
		after, err := newReplier(r.replyAfterSequenceEnded, app, methods)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: after_ended: %w", err)
		}

		s.after = after
		s.own(after)
	}

	return s, nil
}

// sequenceReplier answers the calls of one mock with its replies, in order, one per call, then with after, when set.
type sequenceReplier struct {
	mu      sync.Mutex
	replies []Replier
	after   Replier
	hits    int
	closers []io.Closer // of the replies it owns
}

// Reply implements Replier. Only taking the next reply is locked: the replies run at the same time, as those of any
// mock do.
func (s *sequenceReplier) Reply(ctx context.Context, call *Call) error {
	reply, hits := s.next()
	if reply == nil {
		return fmt.Errorf(
			"reply_sequence: call %d came after the end of the sequence: set OnSequenceEnded or after_ended", hits)
	}

	return reply.Reply(ctx, call)
}

// Close releases the replies the sequence owns.
func (s *sequenceReplier) Close() error {
	return closeAll(s.closers)
}

// next takes the reply of the next call, the next one of the sequence, then after, which can be nil, and the calls
// the sequence took so far, this one included.
func (s *sequenceReplier) next() (Replier, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hits < len(s.replies) {
		reply := s.replies[s.hits]
		s.hits++

		return reply, s.hits
	}

	s.hits++

	return s.after, s.hits
}

// own keeps reply to close with the sequence, when it holds something to release, such as a proxy's connection.
func (s *sequenceReplier) own(reply Replier) {
	if c, ok := reply.(io.Closer); ok {
		s.closers = append(s.closers, c)
	}
}
