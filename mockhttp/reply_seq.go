package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var _ Replier = (*SequentialReply)(nil)

// SequentialReply configures a sequence of repliers to be used after a Mock is matched.
type SequentialReply struct {
	replyAfterSequenceEnded Replier
	replies                 []Replier
	hits                    int32
	rwMutex                 sync.RWMutex
}

// Seq creates a new SequentialReply.
func Seq(reply ...Replier) *SequentialReply {
	return &SequentialReply{replies: reply}
}

// OnSequenceEnded sets a replier to be used once the sequence is over.
func (r *SequentialReply) OnSequenceEnded(reply Replier) *SequentialReply {
	r.replyAfterSequenceEnded = reply
	return r
}

// Add adds a new replier to the sequence.
func (r *SequentialReply) Add(reply ...Replier) *SequentialReply {
	r.replies = append(r.replies, reply...)
	return r
}

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (r *SequentialReply) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	r.rwMutex.Lock()
	defer r.rwMutex.Unlock()
	size := int32(len(r.replies))
	var reply Replier
	if r.hits < size && r.hits >= 0 {
		reply = r.replies[r.hits]
	}
	if reply == nil {
		if r.replyAfterSequenceEnded != nil {
			return r.replyAfterSequenceEnded.Reply(ctx, input)
		}
		return nil, fmt.Errorf(
			"reply_sequence: unable to obtain a response, and no default response was set. request number=%d, sequence size=%d",
			r.hits, size)
	}
	atomic.AddInt32(&r.hits, 1)
	return reply.Reply(ctx, input)
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

func (r *SequentialReply) beforeBuild(app *Server) error {
	size := len(r.replies)
	if size == 0 {
		return errors.New("reply_sequence: you need to set at least one response when using multiple sequential reply")
	}

	for _, reply := range r.replies {
		if err := prepareReply(reply, app); err != nil {
			return err
		}
	}

	return prepareReply(r.replyAfterSequenceEnded, app)
}

func (r *SequentialReply) totalHits() int {
	r.rwMutex.RLock()
	defer r.rwMutex.RUnlock()

	return int(r.hits)
}
