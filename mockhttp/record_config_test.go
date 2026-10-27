package mockhttp

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

func TestRecordConfig_AFilterMatchesPathParams(t *testing.T) {
	// A filter's path params are matchers a mock gets when it builds: the filter took the builder's other matchers
	// only, so a filter on /users/{id} with id 7 recorded /users/8 too.
	target := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "target") })

	m := mockedServer(t, Setup().Silent())
	require.NoError(t, m.StartRecording(
		RecordDir(t.TempDir()),
		RecordTarget(target.URL),
		RecordFilter(Request().URLPathTemplate("/users/{id}").PathParam("id", match.Equal("7"))),
	))

	_, body := do(t, http.MethodGet, m.URL("/users/7"), nil, nil)
	require.Equal(t, "target", string(body))

	_, body = do(t, http.MethodGet, m.URL("/users/8"), nil, nil)
	require.Equal(t, "mock", string(body))
}

func TestRecordConfig_AFilterWithAPathParamOfNoTemplateFailsTheStart(t *testing.T) {
	// A path param needs a template that has it, in a filter as in a mock: the filter dropped it, and recorded every
	// path.
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	err := m.StartRecording(RecordDir(t.TempDir()), RecordFilter(Request().PathParam("id", match.Equal("7"))))
	require.EqualError(t, err, `recorder: filter: path param "id": the mock has no path template: set one with URLPathTemplate`)
	require.False(t, m.IsRecording())
}
