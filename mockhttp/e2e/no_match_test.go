package e2e

import (
	"io"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// unmatchedServer starts a server with setup and one mock, users, that a GET of /users without X-Tenant: a misses by
// its header.
func unmatchedServer(t *testing.T, setup *mockhttp.ConfigBuilder) *mockhttp.Server {
	t.Helper()

	m := mockhttp.MustNew(setup.Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/users").ID("users").Header("X-Tenant", match.Equal("a")).Reply(mockhttp.OK()))

	return m
}

// getWith sends a GET of url with header, and returns the response with its body read.
func getWith(t *testing.T, url string, header http.Header) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	maps.Copy(req.Header, header)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, string(body)
}

func TestNoMatch_TellsWhereTheJournalHasTheRequest(t *testing.T) {
	// An agent or a person that reads the answer can fetch the whole request, its headers and body included, from the
	// link the answer gives: the admin API listens on its own address.
	m := unmatchedServer(t, mockhttp.Setup().Journal("").Admin(admin.MustNew()))

	res, body := getWith(t, m.URL("/users"), nil)
	link := m.AdminURL() + "/requests/" + res.Header.Get(vlatte.HeaderRequestID)
	require.Contains(t, body, "\n- See the request in the journal: `GET "+link+"`\n")

	entry, _ := getWith(t, link, nil)
	require.Equal(t, http.StatusOK, entry.StatusCode)
}

func TestNoMatch_PointsToNoJournalItDoesNotKeep(t *testing.T) {
	// A link to a journal the server doesn't keep would send the reader to a 404.
	m := unmatchedServer(t, mockhttp.Setup().Admin(admin.MustNew()))

	_, body := getWith(t, m.URL("/users"), nil)
	require.NotContains(t, body, "journal")
}

func TestNoMatch_KeepsTheVaryOfCORS(t *testing.T) {
	// A browser's cache keys a CORS answer by Origin: negotiating the format adds Accept to Vary, and keeps Origin.
	m := unmatchedServer(t, mockhttp.Setup().CORS())

	res, _ := getWith(t, m.URL("/users"), http.Header{"Origin": {"https://example.org"}})
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.ElementsMatch(t, []string{"Origin", "Accept"}, res.Header.Values("Vary"))
}

func TestNoMatch_ACustomRendererAnswers(t *testing.T) {
	// A team whose clients expect their own error shape answers in it, with the mocks that came closest, as the
	// journal records them. The VLatte-Error header stays, so tests still tell the answer from a mock's.
	var got []vlatte.NearMiss

	renderer := noMatchFunc(func(w http.ResponseWriter, nm *mockhttp.NoMatch) {
		got = nm.NearMisses
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"NOT_FOUND"}`))
	})

	m := unmatchedServer(t, mockhttp.Setup().Journal("").NoMatchRenderer(renderer))

	res, body := getWith(t, m.URL("/users"), nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(vlatte.HeaderFailure))
	require.JSONEq(t, `{"code":"NOT_FOUND"}`, body)

	page, err := m.Journal().Find(t.Context(), journal.Query{Path: match.Equal("/users")})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)

	entry := page.Entries[0]
	require.Equal(t, journal.OutcomeUnmatched, entry.Outcome)
	require.Equal(t, http.StatusNotFound, entry.Status)
	require.Equal(t, got, entry.NearMisses)
	require.Len(t, got, 1)
}

// noMatchFunc is a NoMatchRenderer of a function.
type noMatchFunc func(w http.ResponseWriter, nm *mockhttp.NoMatch)

func (f noMatchFunc) RenderNoMatch(w http.ResponseWriter, nm *mockhttp.NoMatch) { f(w, nm) }
