package mockhttp

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/mcpwire"
)

func TestRequestValues_CloneCopiesEveryField(t *testing.T) {
	// A Func reply gets a clone of the request's values: a field Clone forgot would be missing there. Every field is
	// set, so a field added later fails here until the test, and Clone, set it.
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)

	r := &RequestValues{
		ReqID:           "id",
		StartedAtTime:   time.Unix(1, 0),
		RawRequest:      &http.Request{Method: http.MethodPost},
		URL:             &url.URL{Path: "/x"},
		URLPathSegments: []string{"x"},
		RawBodyBytes:    []byte("body"),
		ParsedBodyData:  "body",
		App:             app,
		Mock:            &Mock{},
		Explanation:     vlatte.NewExplanation(),
		Data:            map[string]any{"a": 1},
		bodyParser:      &plainTextParser{},
		nearMisses:      []vlatte.NearMiss{{MockID: "m"}},
		scenarioMoves:   []matchscenario.Move{{}},
		journal:         &journalCall{},
		answer:          &answerWriter{},
	}
	r.mcp.Store(&mcpDecoded{msg: &mcpwire.Message{Method: "ping"}})

	v := reflect.ValueOf(r).Elem()
	for i := range v.NumField() {
		require.False(t, v.Field(i).IsZero(), "set %s in this test, and copy it in Clone", v.Type().Field(i).Name)
	}

	c := r.Clone()
	require.Equal(t, r, c)
	require.NotSame(t, r, c)

	// The clone is a value of its own: setting its fields leaves r as it was.
	c.RawRequest = &http.Request{Method: http.MethodGet}
	c.mcp.Store(nil)
	require.Equal(t, http.MethodPost, r.RawRequest.Method)
	require.NotNil(t, r.mcp.Load())
}
