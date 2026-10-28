package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"

	"github.com/caffeine-projects/vlatte/descriptor"
)

var (
	_         ReplierFactory = (*RandomReply)(nil)
	_         Replier        = (*randomReplier)(nil)
	_         io.Closer      = (*randomReplier)(nil)
	_randomMu sync.Mutex
)

// RandomReply configures replies served at random, one per request. It is a ReplierFactory: each mock built with it
// picks from replies of its own.
type RandomReply struct {
	replies []ReplierFactory
	random  *rand.Rand // from RandWith: every mock built with the reply picks with it
	seed    int64
	seeded  bool
}

// Rand initializes a new RandomReply.
func Rand(reply ...ReplierFactory) *RandomReply {
	return &RandomReply{
		replies: reply,
	}
}

// RandWith creates a new RandomReply that picks replies with the given *rand.Rand. Every mock built with the reply
// picks with it.
func RandWith(random *rand.Rand, reply ...ReplierFactory) *RandomReply {
	r := Rand(reply...)
	r.random = random
	return r
}

// RandWithSeed creates a new RandomReply that picks replies in a sequence reproducible by the given seed. Each mock
// built with the reply picks with a generator of its own, from the seed, so each one picks in the same order.
func RandWithSeed(seed int64, reply ...ReplierFactory) *RandomReply {
	r := Rand(reply...)
	r.seed = seed
	r.seeded = true
	return r
}

// Add adds a new Replier to the random list.
func (rep *RandomReply) Add(reply ...ReplierFactory) *RandomReply {
	rep.replies = append(rep.replies, reply...)
	return rep
}

// Describe implements [descriptor.SelfDescriptive]: the reply in the mock file format. It leaves out the replies that
// don't implement [descriptor.SelfDescriptive], and the generator of [RandWith].
func (rep *RandomReply) Describe() descriptor.Descriptor {
	replies := make([]any, 0, len(rep.replies))
	for _, v := range rep.replies {
		if sd, ok := v.(descriptor.SelfDescriptive); ok {
			replies = append(replies, sd.Describe())
		}
	}

	desc := map[string]any{"responses": replies}
	if rep.seeded {
		desc["seed"] = rep.seed
	}

	return descriptor.New("response_random", desc)
}

// NewReplier implements ReplierFactory. It makes the Replier of each reply, so each mock picks from replies of its own.
func (rep *RandomReply) NewReplier(app *Server) (Replier, error) {
	if len(rep.replies) == 0 {
		return nil, errors.New("reply_random: you need to set at least one response when using random reply")
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

		reply, err := newReplier(f, app)
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("reply_random: response %d: %w", i, err)
		}

		r.replies[i] = reply
		if c := owned(f, reply); c != nil {
			r.closers = append(r.closers, c)
		}
	}

	return r, nil
}

// randomReplier serves one of its replies at random, for one mock.
type randomReplier struct {
	replies []Replier
	random  *rand.Rand  // nil picks with the global generator
	closers []io.Closer // of the replies it owns
}

// Reply implements Replier.
func (r *randomReplier) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
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

	return r.replies[index].Reply(ctx, input)
}

// Close releases the replies it owns.
func (r *randomReplier) Close() error {
	return closeAll(r.closers)
}
