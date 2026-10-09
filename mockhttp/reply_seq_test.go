package mockhttp

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSequentialReply(t *testing.T) {
	t.Parallel()

	t.Run("should return replies based configure sequence and return error when over", func(t *testing.T) {
		t.Parallel()

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}
		input := ReplyInput{RequestValues: rv, Writer: nil}
		builder := replierOf(t, Seq(InternalServerError(), BadRequest(), OK(), NotFound()))

		res, err := builder.Reply(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)

		res, err = builder.Reply(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)

		res, err = builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)

		res, err = builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusNotFound, res.StatusCode)

		_, err = builder.Reply(context.Background(), input)
		assert.NotNil(t, err)
	})

	t.Run("should return the configured reply when the sequence has ended", func(t *testing.T) {
		t.Parallel()

		req, err := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
		rv := &RequestValues{RawRequest: req, URL: req.URL}
		require.NoError(t, err)
		input := ReplyInput{RequestValues: rv, Writer: nil}
		builder := replierOf(t, Seq().Add(OK()).OnSequenceEnded(NotFound()))

		res, err := builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)

		res, err = builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}

func TestSequentialReplyShouldReturnErrorWhenSequenceDoesNotContainReplies(t *testing.T) {
	r, err := Seq().NewReplier(nil)
	assert.Nil(t, r)
	assert.NotNil(t, err)
}

func TestSequentialReply_PreparesNestedReplies(t *testing.T) {
	target := MustNew()
	target.MustStart()
	t.Cleanup(target.Close)
	target.MustMock(Getf("/seq").Reply(Accepted()))

	m := MustNew()
	m.MustStart()
	t.Cleanup(m.Close)
	// From creates its HTTP client in NewReplier: Seq must make the Replier of every reply it holds.
	m.MustMock(Getf("/seq").Reply(Seq(From(target.URL())).OnSequenceEnded(From(target.URL()))))

	// The second request is served by the reply set with OnSequenceEnded.
	for range 2 {
		res, err := http.Get(m.URL("/seq"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusAccepted, res.StatusCode)
	}
}

func TestSequentialReplyValidate(t *testing.T) {
	seq := Seq()

	_, err := seq.NewReplier(nil)
	require.Error(t, err)

	seq.Add(OK())

	_, err = seq.NewReplier(nil)
	require.NoError(t, err)
}

func TestSeqRace(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL}
	builder, ok := replierOf(t, Seq(InternalServerError(), BadRequest(), OK(), NotFound())).(*sequenceReplier)
	require.True(t, ok)

	jobs := 3
	wg := sync.WaitGroup{}

	input := ReplyInput{RequestValues: rv, Writer: nil}
	for range jobs {
		wg.Go(func() {
			res, err := builder.Reply(context.Background(), input)
			if assert.NoError(t, err) {
				assert.True(t, res.StatusCode != StatusNoMatch)
			}

			builder.totalHits()
		})

		builder.totalHits()
	}

	res, err := builder.Reply(context.Background(), input)
	require.NoError(t, err)
	require.True(t, res.StatusCode != StatusNoMatch)

	builder.totalHits()

	wg.Wait()
	require.Equal(t, 4, builder.totalHits())
}

func TestSequentialReply_RepliesConcurrently(t *testing.T) {
	// The sequence held its lock while a reply ran, so a slow reply, such as a proxy's, held up every request of the
	// mock behind it. Only taking the next reply is locked now.
	var inFlight sync.WaitGroup
	inFlight.Add(2)
	both := make(chan struct{})
	go func() { inFlight.Wait(); close(both) }()

	// Each step waits until both are running: steps served one at a time never get there.
	inner := Func(func(http.ResponseWriter, *RequestValues) (*Response, error) {
		inFlight.Done()
		select {
		case <-both:
			return &Response{StatusCode: http.StatusOK}, nil
		case <-time.After(2 * time.Second):
			return nil, errors.New("sequence replies were served one at a time")
		}
	})
	rep := replierOf(t, Seq(inner, inner))

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

func TestSequentialReply_EachMockRunsItsOwnSequence(t *testing.T) {
	// #23: a sequence shared by two mocks had one position, so each mock skipped what the other served. Each mock
	// runs the sequence on its own now, from the first reply.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	seq := Seq(OK(), NotFound())
	m.MustMock(Getf("/a").Reply(seq), Getf("/b").Reply(seq))

	steps := []struct {
		path   string
		status int
	}{
		{path: "/a", status: http.StatusOK},
		{path: "/b", status: http.StatusOK},
		{path: "/a", status: http.StatusNotFound},
		{path: "/b", status: http.StatusNotFound},
	}

	for _, s := range steps {
		status, _ := get(t, m, s.path)
		require.Equal(t, s.status, status, s.path)
	}
}
