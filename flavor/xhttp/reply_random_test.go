package xhttp

import (
	"context"
	"math/rand"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRandomReplies(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL, App: New()}
	input := ReplyInput{RequestValues: rv, Writer: nil}
	statuses := []int{
		http.StatusOK, http.StatusInternalServerError, http.StatusCreated, http.StatusBadRequest,
	}

	for range 5000 {
		res, err := Rand(
			BadRequest(),
			OK(),
			Created()).
			Add(InternalServerError()).
			Reply(context.Background(), input)

		contains := slices.Contains(statuses, res.StatusCode)

		require.Nil(t, err)
		require.True(t, contains)
	}
}

func TestShouldReturnErrorWhenRandomDoesNotContainReplies(t *testing.T) {
	require.Error(t, Rand().beforeBuild(nil))
}

func TestRandWithCustom(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://localhost:8080", nil)
	rv := &RequestValues{RawRequest: req, URL: req.URL, App: New()}
	statuses := []int{
		http.StatusOK, http.StatusInternalServerError, http.StatusCreated, http.StatusBadRequest,
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	input := ReplyInput{RequestValues: rv, Writer: nil}
	for range 5000 {
		res, err := RandWith(
			r,
			BadRequest(),
			OK(),
			Created(),
			InternalServerError(),
		).Reply(context.Background(), input)

		contains := slices.Contains(statuses, res.StatusCode)

		require.Nil(t, err)
		require.True(t, contains)
	}
}

func TestRandomReplyValidate(t *testing.T) {
	r := Rand()

	require.Error(t, r.beforeBuild(nil))

	r.Add(OK())

	require.NoError(t, r.beforeBuild(nil))
}
