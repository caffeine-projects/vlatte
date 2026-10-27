package mockgrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
)

var errReplyRandEmpty = errors.New("reply_random: no replies: give Rand one at least")

var (
	_         Reply     = (*RandomReply)(nil)
	_         Replier   = (*randomReplier)(nil)
	_         io.Closer = (*randomReplier)(nil)
	_randomMu sync.Mutex
)

// RandomReply answers each call of a mock with one of its replies, picked at random. A call of any type takes one
// reply, a stream as a whole. Each mock built with it picks from replies of its own.
type RandomReply struct {
	replies []Reply
	random  *rand.Rand // from RandWith: every mock built with the reply picks with it
	seed    int64
	seeded  bool
}

// Rand creates a new RandomReply.
func Rand(reply ...Reply) *RandomReply {
	return &RandomReply{replies: reply}
}

// RandWith creates a new RandomReply that picks replies with random. Every mock built with the reply picks with it.
// A mock with it is described without its generator: a mock file loads it back as a Rand.
func RandWith(random *rand.Rand, reply ...Reply) *RandomReply {
	r := Rand(reply...)
	r.random = random

	return r
}

// RandWithSeed creates a new RandomReply that picks replies in an order the seed repeats. Each mock built with the
// reply picks with a generator of its own, from the seed, so each one picks in the same order.
func RandWithSeed(seed int64, reply ...Reply) *RandomReply {
	r := Rand(reply...)
	r.seed = seed
	r.seeded = true

	return r
}

// Add adds replies to pick from.
func (rep *RandomReply) Add(reply ...Reply) *RandomReply {
	rep.replies = append(rep.replies, reply...)
	return rep
}

// NewReplier implements Reply. It makes the replier of each reply, which checks itself against the methods, so each
// mock picks from replies of its own.
func (rep *RandomReply) NewReplier(app *Server, methods []protoreflect.MethodDescriptor) (Replier, error) {
	if len(rep.replies) == 0 {
		return nil, errReplyRandEmpty
	}

	r := &randomReplier{replies: make([]Replier, len(rep.replies)), random: rep.random}
	if rep.seeded {
		r.random = rand.New(rand.NewPCG(uint64(rep.seed), 0))
	}

	for i, f := range rep.replies {
		if f == nil {
			r.Close()
			return nil, fmt.Errorf("reply_random: response %d is nil", i)
		}

		reply, err := newReplier(f, app, methods)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("reply_random: response %d: %w", i, err)
		}

		r.replies[i] = reply
		if c, ok := reply.(io.Closer); ok {
			r.closers = append(r.closers, c)
		}
	}

	return r, nil
}

// randomReplier answers each call of one mock with one of its replies, at random.
type randomReplier struct {
	replies []Replier
	random  *rand.Rand  // nil picks with the global generator
	closers []io.Closer // of the replies it owns
}

// Reply implements Replier.
func (r *randomReplier) Reply(ctx context.Context, call *Call) error {
	var index int
	if r.random == nil {
		index = rand.IntN(len(r.replies))
	} else {
		// A *rand.Rand is not safe for concurrent use, and the mocks of a RandWith reply share one.
		// The lock guards it only; the chosen reply runs without it.
		_randomMu.Lock()
		index = r.random.IntN(len(r.replies))
		_randomMu.Unlock()
	}

	return r.replies[index].Reply(ctx, call)
}

// Close releases the replies it owns.
func (r *randomReplier) Close() error {
	return closeAll(r.closers)
}
