package xhttp

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

var (
	_         vlatte.Replier[ReplyInput, *Response] = (*RandomReply)(nil)
	_randomMu sync.Mutex
)

// RandomReply configures a Replier that serves random HTTP responses.
type RandomReply struct {
	replies []vlatte.Replier[ReplyInput, *Response]
	random  *rand.Rand
	seed    int64
	seeded  bool
}

// Rand initializes a new RandomReply.
func Rand(reply ...vlatte.Replier[ReplyInput, *Response]) *RandomReply {
	return &RandomReply{
		replies: reply,
	}
}

// RandWith creates a new RandomReply that picks replies with the given *rand.Rand.
func RandWith(random *rand.Rand, reply ...vlatte.Replier[ReplyInput, *Response]) *RandomReply {
	r := Rand(reply...)
	r.random = random
	return r
}

// RandWithSeed creates a new RandomReply that picks replies in a sequence reproducible by the given seed.
func RandWithSeed(seed int64, reply ...vlatte.Replier[ReplyInput, *Response]) *RandomReply {
	r := RandWith(rand.New(rand.NewPCG(uint64(seed), 0)), reply...)
	r.seed = seed
	r.seeded = true
	return r
}

// Add adds a new Replier to the random list.
func (rep *RandomReply) Add(reply ...vlatte.Replier[ReplyInput, *Response]) *RandomReply {
	rep.replies = append(rep.replies, reply...)
	return rep
}

func (rep *RandomReply) beforeBuild(app *HTTPMockApp) error {
	size := len(rep.replies)
	if size == 0 {
		return errors.New("reply_random: you need to set at least one response when using random reply")
	}

	for _, reply := range rep.replies {
		if err := prepareReply(reply, app); err != nil {
			return err
		}
	}

	return nil
}

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (rep *RandomReply) Reply(ctx context.Context, input ReplyInput) (*Response, error) {
	var index int
	if rep.random == nil {
		index = rand.IntN(len(rep.replies))
	} else {
		// A *rand.Rand is not safe for concurrent use and callers may share one between replies.
		// The lock guards it only; the chosen reply runs without it.
		_randomMu.Lock()
		index = rep.random.IntN(len(rep.replies))
		_randomMu.Unlock()
	}

	return rep.replies[index].Reply(ctx, input)
}

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
