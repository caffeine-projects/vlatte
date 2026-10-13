package mockhttp

import (
	json "encoding/json/v2"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

func TestRace(t *testing.T) {
	m := &Mock{}
	jobs := 10
	wg := sync.WaitGroup{}

	for range jobs {
		wg.Go(func() {
			m.Inc()
			m.Hits()
		})

		m.Inc()
	}

	m.Hits()
	m.Inc()
	m.Inc()

	wg.Wait()

	assert.EqualValues(t, (jobs*2)+2, m.Hits())
}

func TestMock(t *testing.T) {
	m, err := Getf("/mock").Reply(OK()).Build(MustNew(Setup().Silent()))
	require.NoError(t, err)

	t.Run("should init enabled", func(t *testing.T) {
		assert.True(t, m.Enabled())
	})

	t.Run("should disable mock when calling .Disable()", func(t *testing.T) {
		m.Disable()
		assert.False(t, m.Enabled())

		m.Enable()
		assert.True(t, m.Enabled())
	})

	t.Run("should return called when it was hit", func(t *testing.T) {
		assert.False(t, m.Called())
		m.Inc()
		assert.True(t, m.Called())
	})
}

func TestMock_MarshalJSON(t *testing.T) {
	app := MustNew()
	builder := Request().
		Header("accept", match.Eq("test")).
		Once().
		ScenarioIs("test").ScenarioStateIs("state").ScenarioStateWillBe("future").
		Reply(OK())

	m, err := builder.Build(app)
	require.NoError(t, err)

	b, err := m.MarshalJSON()
	require.NoError(t, err)

	fmt.Println(string(b))
}

func TestMock_String(t *testing.T) {
	// Failure bodies and matching errors name a mock with String, so it must read the same as assertion messages.
	m := MustNew(Setup().Silent())

	named, err := Getf("/").ID("a").Name("users").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [a] users", named.String())

	unnamed, err := Getf("/").ID("b").Reply(OK()).Build(m)
	require.NoError(t, err)
	require.Equal(t, "Mock [b]", unnamed.String())
}

func TestMock_ADescribedMockLoadsBackAndMatchesTheSame(t *testing.T) {
	// The description of a mock is a mock file, as the admin API shows and takes it. Loaded back, it must match the
	// same requests: a matcher of the method must not get lost, nor one of the path or fragment turn into one of the
	// whole URL.
	tests := []struct {
		name        string
		give        *Builder
		wantKeys    []string
		wantMatch   []string
		wantNoMatch []string
	}{
		{
			name:        "path",
			give:        Getf("/test"),
			wantKeys:    []string{"method", "path"},
			wantMatch:   []string{"/test"},
			wantNoMatch: []string{"/other"},
		},
		{
			// The requests are GETs.
			name:        "method",
			give:        Postf("/test"),
			wantKeys:    []string{"method", "path"},
			wantMatch:   []string{},
			wantNoMatch: []string{"/test"},
		},
		{
			name:        "path and fragment",
			give:        Getf("/test").URLFragmentf("contacts"),
			wantKeys:    []string{"fragment", "method", "path"},
			wantMatch:   []string{"/test#contacts"},
			wantNoMatch: []string{"/test#other", "/test"},
		},
		{
			// A mock file has url or path, not both: the path matcher goes in url.
			name:        "url and path",
			give:        Request().URL(match.Contain("test")).URLPathf("/test"),
			wantKeys:    []string{"url"},
			wantMatch:   []string{"/test"},
			wantNoMatch: []string{"/x/test", "/other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built := MustNew(Setup().Silent())
			built.MustStart()
			t.Cleanup(built.Close)

			desc, err := built.MustMock(tt.give.Reply(OK())).Mocks()[0].MarshalJSON()
			require.NoError(t, err)

			var doc map[string]any
			require.NoError(t, json.Unmarshal(desc, &doc))
			request, _ := doc["request"].(map[string]any)
			require.Equal(t, tt.wantKeys, slices.Sorted(maps.Keys(request)), string(desc))

			loaded := MustNew(Setup().Silent())
			loaded.MustStart()
			t.Cleanup(loaded.Close)
			loaded.MustMock(FromBytes(desc, "json"))

			for _, target := range tt.wantMatch {
				require.Equal(t, http.StatusOK, getTarget(t, built, target), target)
				require.Equal(t, http.StatusOK, getTarget(t, loaded, target), "loaded: %s: %s", target, desc)
			}

			for _, target := range tt.wantNoMatch {
				require.Equal(t, StatusNoMatch, getTarget(t, built, target), target)
				require.Equal(t, StatusNoMatch, getTarget(t, loaded, target), "loaded: %s: %s", target, desc)
			}
		})
	}
}

// getTarget sends a GET with target as the request target, as is, and returns the status: Go's http.Client doesn't
// send a URL's fragment.
func getTarget(t *testing.T, m *Server, target string) int {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, m.URL(), nil)
	require.NoError(t, err)
	req.URL.Opaque = target

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode
}
