package xhttp

import (
	"context"
	"net/http"
	"sync"
	"testing"

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
		builder := Seq(InternalServerError(), BadRequest(), OK(), NotFound())

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
		builder := Seq().Add(OK()).OnSequenceEnded(NotFound())

		res, err := builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode)

		res, err = builder.Reply(context.Background(), input)
		assert.Nil(t, err)
		assert.Equal(t, http.StatusNotFound, res.StatusCode)
	})
}

func TestSequentialReplyShouldReturnErrorWhenSequenceDoesNotContainReplies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL}
	res, err := Seq().Reply(context.Background(), ReplyInput{RequestValues: rv, Writer: nil})
	assert.Nil(t, res)
	assert.NotNil(t, err)
}

func TestSequentialReply_PreparesNestedReplies(t *testing.T) {
	target := New()
	target.MustStart()
	t.Cleanup(target.Close)
	target.MustMock(Getf("/seq").Reply(Accepted()))

	m := New()
	m.MustStart()
	t.Cleanup(m.Close)
	// From creates its HTTP client in beforeBuild: Seq must prepare every reply it holds.
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

	require.Error(t, seq.beforeBuild(nil))

	seq.Add(OK())

	require.NoError(t, seq.beforeBuild(nil))
}

func TestSeqRace(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL}
	builder := Seq(InternalServerError(), BadRequest(), OK(), NotFound())

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
