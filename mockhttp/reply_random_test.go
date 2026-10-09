package mockhttp

import (
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRandomReplies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL, App: MustNew()}
	input := ReplyInput{RequestValues: rv, Writer: nil}
	statuses := []int{
		http.StatusOK, http.StatusInternalServerError, http.StatusCreated, http.StatusBadRequest,
	}

	replier := replierOf(t, Rand(BadRequest(), OK(), Created()).Add(InternalServerError()))

	for range 5000 {
		res, err := replier.Reply(t.Context(), input)

		contains := slices.Contains(statuses, res.StatusCode)

		require.Nil(t, err)
		require.True(t, contains)
	}
}

func TestShouldReturnErrorWhenRandomDoesNotContainReplies(t *testing.T) {
	_, err := Rand().NewReplier(nil)
	require.Error(t, err)
}

func TestRandWithCustom(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL, App: MustNew()}
	statuses := []int{
		http.StatusOK, http.StatusInternalServerError, http.StatusCreated, http.StatusBadRequest,
	}

	r := rand.New(rand.NewPCG(1, 2))

	input := ReplyInput{RequestValues: rv, Writer: nil}
	replier := replierOf(t, RandWith(r, BadRequest(), OK(), Created(), InternalServerError()))

	for range 5000 {
		res, err := replier.Reply(t.Context(), input)

		contains := slices.Contains(statuses, res.StatusCode)

		require.Nil(t, err)
		require.True(t, contains)
	}
}

func TestRandomReply_PreparesNestedReplies(t *testing.T) {
	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)
	// Body templates compile in NewReplier: Rand must make the Replier of every reply it holds.
	m.MustMock(Getf("/rand").Reply(Rand(OK().BodyTemplate("{{ .Request.Method }}"))))

	res, err := http.Get(m.URL("/rand"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "GET", string(b))
}

func TestRandomReply_DescribeIncludesSeed(t *testing.T) {
	b, err := RandWithSeed(42, OK()).Describe().MarshalJSON()

	require.NoError(t, err)
	require.Contains(t, string(b), `"seed":42`)
}

func TestRandomReply_RepliesConcurrently(t *testing.T) {
	var inFlight sync.WaitGroup
	inFlight.Add(2)
	both := make(chan struct{})
	go func() { inFlight.Wait(); close(both) }()

	// Each inner reply waits until both are running: replies served one at a time never get there.
	inner := Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
		inFlight.Done()
		select {
		case <-both:
			return &Response{StatusCode: http.StatusOK}, nil
		case <-time.After(2 * time.Second):
			return nil, errors.New("random replies were served one at a time")
		}
	})
	rep := replierOf(t, RandWithSeed(1, inner))

	req, _ := http.NewRequest(http.MethodGet, "http://localhost", nil)
	input := ReplyInput{RequestValues: &RequestValues{RawRequest: req, URL: req.URL}}

	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := rep.Reply(t.Context(), input)
			errs <- err
		}()
	}

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
}

func TestRandomReplyValidate(t *testing.T) {
	r := Rand()

	_, err := r.NewReplier(nil)
	require.Error(t, err)

	r.Add(OK())

	_, err = r.NewReplier(nil)
	require.NoError(t, err)
}

func TestRandomReply_SeededPicksInTheSameOrderOnEachServer(t *testing.T) {
	// A seeded reply gives each mock a generator of its own, from the seed, so two servers serving it pick in the same
	// order, whatever the other serves. Before, their mocks drew from one generator.
	rep := RandWithSeed(7, OK(), Created(), Accepted(), NoContent())

	s1, s2 := MustNew(Setup().Silent()), MustNew(Setup().Silent())
	for _, s := range []*Server{s1, s2} {
		s.MustStart()
		t.Cleanup(s.Close)
		s.MustMock(Getf("/r").Reply(rep))
	}

	first := make([]int, 0, 10)
	second := make([]int, 0, 10)

	for range 10 {
		status, _ := get(t, s1, "/r")
		first = append(first, status)

		status, _ = get(t, s2, "/r")
		second = append(second, status)
	}

	require.Equal(t, first, second)
}
