package e2e

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestSequenceReplies(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Reply(mockhttp.Seq().
			Add(mockhttp.Unauthorized(), mockhttp.OK())))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	for range 3 {
		res, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusTeapot, res.StatusCode)
	}
}

func TestSequenceRepliesOnSequenceEndsSet(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.Get(match.URLPath("/test")).
		Reply(mockhttp.Seq().
			Add(mockhttp.Unauthorized(), mockhttp.OK()).
			OnSequenceEnded(mockhttp.Created())))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	for range 2 {
		res, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
}

func TestSequence_SetupFromFile(t *testing.T) {
	type response struct {
		Ok   bool    `json:"ok,omitzero"`
		Type string  `json:"type,omitempty"`
		Num  float64 `json:"num,omitzero"`
	}

	m := mockhttp.MustNew()
	m.MustStart()

	defer m.Close()

	m.MustMock(mockhttp.FromFile("testdata/response_sequence/seq_01.yaml"))

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test", nil)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	r := new(response)
	err = json.Unmarshal(b, r)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, true, r.Ok)
	require.Equal(t, "test", r.Type)
	require.EqualValues(t, 10, r.Num)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusBadRequest, res.StatusCode)

	for range 3 {
		res, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	}
}
