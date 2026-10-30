package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestJSONBody_ABodylessGETReachesTheLaterGETMock(t *testing.T) {
	// Mocha #62: an earlier POST mock tried to match the absent body of a GET and panicked.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Postf("/test").Body(match.JSONPath("data", match.Contain("aaa"))).
		Reply(mockhttp.OK().BodyText("post")))
	m.MustMock(mockhttp.Getf("/test").Reply(mockhttp.OK().BodyText("get")))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL("/test"), nil)
	require.NoError(t, err)
	res, body := do(t, req)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "get", string(body))
}

func TestJSONBody_ABodylessPOSTDoesNotMatchAndTheServerKeepsServing(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(mockhttp.Postf("/test").Body(match.JSONPath("data", match.Contain("aaa"))).
		Reply(mockhttp.OK().BodyText("post")))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL("/test"), nil)
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
	res, _ := do(t, req)
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(mockhttp.HeaderVlatteError))

	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL("/test"), strings.NewReader(`{"data":"aaa"}`))
	require.NoError(t, err)
	req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
	res, body := do(t, req)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "post", string(body))
}

func TestJSONPath_AMissingFieldLetsTheNextMockMatch(t *testing.T) {
	// Mocha #63: an absent JSONPath field was an error, which stopped the search before the matching mock.
	for _, path := range []string{"/party/edit", "/document/edit"} {
		t.Run(path, func(t *testing.T) {
			m := mockhttp.MustNew(mockhttp.Setup().Silent())
			m.MustStart()
			t.Cleanup(m.Close)
			m.MustMock(mockhttp.Postf("/document/edit").Body(match.JSONPath("document_id", match.Equal(1337))).
				Reply(mockhttp.OK().BodyText("document")))
			m.MustMock(mockhttp.Postf("%s", path).Body(match.JSONPath("party_id", match.Equal(1337))).
				Reply(mockhttp.OK().BodyText("party")))

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL("/document/edit"),
				strings.NewReader(`{"document_id":1337}`))
			require.NoError(t, err)
			req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
			res, body := do(t, req)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "document", string(body))

			req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL(path), strings.NewReader(`{"party_id":1337}`))
			require.NoError(t, err)
			req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
			res, body = do(t, req)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, "party", string(body))
		})
	}
}
