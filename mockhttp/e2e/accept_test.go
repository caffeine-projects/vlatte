package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestAccept_TheClientsPreferencePicksTheRepresentation(t *testing.T) {
	// A mock per representation, as a generator writes them from a spec: the client's q-values pick the one that
	// answers, not the order of the mocks, and a client that takes none of them gets the 406.
	m := mockhttp.MustNew(mockhttp.Setup().MockFilePatterns("testdata/accept/*.mock.yaml"))
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		accept []string
		status int
		mock   string
	}{
		{accept: nil, status: http.StatusOK, mock: "pets.json"},
		{accept: []string{"application/json"}, status: http.StatusOK, mock: "pets.json"},
		{accept: []string{"application/xml, application/json;q=0.1"}, status: http.StatusOK, mock: "pets.xml"},
		{accept: []string{"application/json;q=0.5", "application/xml"}, status: http.StatusOK, mock: "pets.xml"},
		{accept: []string{"*/*"}, status: http.StatusOK, mock: "pets.json"},
		{accept: []string{"text/html"}, status: http.StatusNotAcceptable, mock: "pets.406"},
		{accept: []string{"*/*;q=0"}, status: http.StatusNotAcceptable, mock: "pets.406"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.accept, "; "), func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+"/pets", nil)
			require.NoError(t, err)

			for _, v := range tt.accept {
				req.Header.Add("Accept", v)
			}

			// Go's client sends no Accept of its own, so nil sends none.
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			_, err = io.Copy(io.Discard, res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			require.Equal(t, tt.status, res.StatusCode)
			require.Equal(t, tt.mock, res.Header.Get(vlatte.HeaderMatchedMockID))
		})
	}
}

func TestAccept_A406MockWithoutOffersDecodesBack(t *testing.T) {
	// NotAcceptable without offers matches every Accept that parses: a store that keeps its mock as EncodeMock
	// describes it builds the same mock back with DecodeMock.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	t.Cleanup(m.Close)

	mock, err := mockhttp.Getf("/pets").ID("pets.406").
		Headers("Accept", match.NotAcceptable()).
		Reply(mockhttp.NewReply().Status(http.StatusNotAcceptable)).
		Build(m)
	require.NoError(t, err)

	data, err := mockhttp.EncodeMock(mock, "json")
	require.NoError(t, err)

	back, err := mockhttp.DecodeMock(m, data, "json")
	require.NoError(t, err)

	again, err := mockhttp.EncodeMock(back, "json")
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(again))
}
