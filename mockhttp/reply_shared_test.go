package mockhttp

import (
	"context"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

// counting answers with how many requests it has served.
type counting struct {
	n atomic.Int64
}

func (c *counting) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusOK, Body: strconv.AppendInt(nil, c.n.Add(1), 10)}, nil
}

// countingFactory gives each mock a count of its own.
type countingFactory struct{}

func (countingFactory) NewReplier(*Server) (Replier, error) {
	return &counting{}, nil
}

func TestShared_OneReplierForEveryMock(t *testing.T) {
	// What a Replier keeps belongs to its mock, because a factory makes one Replier for each mock. Shared gives one
	// Replier to every mock built with it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	shared := &counting{}
	m.MustMock(
		Getf("/own/a").Reply(countingFactory{}),
		Getf("/own/b").Reply(countingFactory{}),
		Getf("/shared/a").Reply(Shared(shared)),
		Getf("/shared/b").Reply(Shared(shared)),
	)

	steps := []struct {
		path string
		want string
	}{
		{path: "/own/a", want: "1"},
		{path: "/own/b", want: "1"},
		{path: "/own/a", want: "2"},
		{path: "/shared/a", want: "1"},
		{path: "/shared/b", want: "2"},
	}

	for _, s := range steps {
		_, body := get(t, m, s.path)
		require.Equal(t, s.want, body, s.path)
	}
}

// teapot replies 418 and describes itself.
type teapot struct{}

func (teapot) Reply(context.Context, ReplyInput) (*Response, error) {
	return &Response{StatusCode: http.StatusTeapot}, nil
}

func (teapot) Describe() vlatte.Descriptor {
	return vlatte.NewDescriptor("teapot", map[string]any{"short": true})
}

func TestShared_DescribesAsItsReplier(t *testing.T) {
	// A mock describes its reply through its factory, and Shared describes as its Replier does.
	m := MustNew(Setup().Silent())

	mock, err := Getf("/tea").Reply(Shared(teapot{})).Build(m)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"short": true}, mock.Describe().Props.(map[string]any)["teapot"])
}

func TestShared_NilFailsTheBuildAtAnyDepth(t *testing.T) {
	// A factory that made no Replier failed the build only as the reply of the mock: inside Seq or Rand the mock
	// built, and its first request panicked on the nil Replier. It fails the build at any depth now, and says where.
	m := MustNew(Setup().Silent())

	tests := []struct {
		name  string
		reply ReplierFactory
		want  string
	}{
		{
			name:  "reply",
			reply: Shared(nil),
			want:  "reply: the factory made no replier",
		},
		{
			name:  "in a sequence",
			reply: Seq(OK(), Shared(nil)),
			want:  "reply_sequence: response 1: reply: the factory made no replier",
		},
		{
			name:  "after a sequence",
			reply: Seq(OK()).OnSequenceEnded(Shared(nil)),
			want:  "reply_sequence: after_ended: reply: the factory made no replier",
		},
		{
			name:  "random",
			reply: Rand(OK(), Shared(nil)),
			want:  "reply_random: response 1: reply: the factory made no replier",
		},
	}

	for _, tc := range tests {
		_, err := Getf("/nil").Reply(tc.reply).Build(m)
		require.ErrorIs(t, err, errNoReplier, tc.name)
		require.EqualError(t, err, tc.want, tc.name)
	}
}
