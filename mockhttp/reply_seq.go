package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_ ReplierFactory = (*SequentialReply)(nil)
	_ Replier        = (*sequenceReplier)(nil)
	_ io.Closer      = (*sequenceReplier)(nil)
)

// SequentialReply configures a sequence of replies, served one per request, in order, then the one set with
// OnSequenceEnded. It is a ReplierFactory: each mock built with it runs the sequence on its own, from the first reply.
type SequentialReply struct {
	replyAfterSequenceEnded ReplierFactory
	replies                 []ReplierFactory
}

// Seq creates a new SequentialReply.
func Seq(reply ...ReplierFactory) *SequentialReply {
	return &SequentialReply{replies: reply}
}

// OnSequenceEnded sets a replier to be used once the sequence is over.
func (r *SequentialReply) OnSequenceEnded(reply ReplierFactory) *SequentialReply {
	r.replyAfterSequenceEnded = reply
	return r
}

// Add adds a new replier to the sequence.
func (r *SequentialReply) Add(reply ...ReplierFactory) *SequentialReply {
	r.replies = append(r.replies, reply...)
	return r
}

func (r *SequentialReply) Describe() descriptor.Descriptor {
	replies := make([]any, 0, len(r.replies))
	for _, v := range r.replies {
		if sd, ok := v.(descriptor.SelfDescriptive); ok {
			replies = append(replies, sd.Describe())
		}
	}

	desc := map[string]any{"responses": replies}

	if r.replyAfterSequenceEnded != nil {
		if sd, ok := r.replyAfterSequenceEnded.(descriptor.SelfDescriptive); ok {
			desc["after_ended"] = sd.Describe()
		}
	}

	return descriptor.New("response_sequence", desc)
}

// NewReplier implements ReplierFactory. It makes the Replier of each reply of the sequence, so each mock has replies
// and a place in the sequence of its own.
func (r *SequentialReply) NewReplier(app *Server) (Replier, error) {
	if len(r.replies) == 0 {
		return nil, errors.New("reply_sequence: you need to set at least one response when using multiple sequential reply")
	}

	s := &sequenceReplier{replies: make([]Replier, len(r.replies))}
	for i, f := range r.replies {
		if f == nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: response %d is nil", i)
		}

		reply, err := newReplier(f, app)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: response %d: %w", i, err)
		}

		s.replies[i] = reply
		s.own(f, reply)
	}

	if r.replyAfterSequenceEnded != nil {
		after, err := newReplier(r.replyAfterSequenceEnded, app)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("reply_sequence: after_ended: %w", err)
		}

		s.after = after
		s.own(r.replyAfterSequenceEnded, after)
	}

	return s, nil
}

// sequenceReplier serves its replies in order, one per request, for one mock, then after, when set.
type sequenceReplier struct {
	mu      sync.Mutex
	replies []Replier
	after   Replier
	hits    int
	closers []io.Closer // of the replies it owns
}

// Reply implements Replier. Only taking the next reply is locked: the replies run at the same time, as those of any
// mock do.
func (s *sequenceReplier) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	reply := s.next()
	if reply == nil {
		return nil, fmt.Errorf(
			"reply_sequence: unable to obtain a response, and no default response was set. request number=%d, sequence size=%d",
			s.totalHits(), len(s.replies))
	}

	return reply.Reply(ctx, input)
}

// Close releases the replies the sequence owns.
func (s *sequenceReplier) Close() error {
	return closeAll(s.closers)
}

// next takes the reply of the next request: the next one of the sequence, then after, which can be nil.
func (s *sequenceReplier) next() Replier {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hits < len(s.replies) {
		reply := s.replies[s.hits]
		s.hits++

		return reply
	}

	return s.after
}

// own keeps what releases reply, which f made, when the sequence owns it.
func (s *sequenceReplier) own(f ReplierFactory, reply Replier) {
	if c := owned(f, reply); c != nil {
		s.closers = append(s.closers, c)
	}
}

func (s *sequenceReplier) totalHits() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hits
}
