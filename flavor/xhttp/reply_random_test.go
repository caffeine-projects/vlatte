package xhttp

import (
	"context"
	"io"
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

func TestRandomReply_PreparesNestedReplies(t *testing.T) {
	m := New()
	m.MustStart()
	t.Cleanup(m.Close)
	// Body templates are parsed in beforeBuild: Rand must prepare every reply it holds.
	m.MustMock(Getf("/rand").Reply(Rand(OK().BodyTemplate("{{ .Request.Method }}"))))

	res, err := http.Get(m.URL("/rand"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "GET", string(b))
}

func TestRandomReplyValidate(t *testing.T) {
	r := Rand()

	require.Error(t, r.beforeBuild(nil))

	r.Add(OK())

	require.NoError(t, r.beforeBuild(nil))
}
