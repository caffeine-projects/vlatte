package mockhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunctionReply(t *testing.T) {
	fn := func(http.ResponseWriter, *RequestValues) (*Response, error) {
		return &Response{StatusCode: http.StatusAccepted}, nil
	}

	r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
	rv := &RequestValues{RawRequest: r, URL: r.URL}
	res, err := replierOf(t, Func(fn)).Reply(t.Context(), ReplyInput{Writer: nil, RequestValues: rv})

	assert.Nil(t, err)
	assert.Equal(t, res.StatusCode, http.StatusAccepted)
}

func TestHandlerReply(t *testing.T) {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}

	r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
	rv := &RequestValues{RawRequest: r, URL: r.URL}
	w := httptest.NewRecorder()

	res, err := replierOf(t, Handler(fn)).Reply(t.Context(), ReplyInput{Writer: w, RequestValues: rv})

	assert.NoError(t, err)
	assert.Nil(t, res)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestSteps_AStepCanEndAHandlerThatWaitsOnItsRequest(t *testing.T) {
	// A handler stops when its request's context ends, as for any server: a step that times the response out ends it
	// too, else the handler holds the client until it leaves.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	errTimeout := errors.New("step: timed out")
	release := make(chan struct{})
	causes := make(chan error, 1)
	scoped := m.MustMock(Getf("/wait").Step(abortOn{release: release, err: errTimeout}).Reply(Handler(func(_ http.ResponseWriter, r *http.Request) {
		close(release)
		<-r.Context().Done()
		causes <- context.Cause(r.Context())
	})))

	// The guard only bounds a failure.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL("/wait"), nil)
	require.NoError(t, err)

	_, err = http.DefaultClient.Do(req)
	require.ErrorIs(t, err, io.EOF)
	require.ErrorIs(t, <-causes, errTimeout)
	require.EqualValues(t, 1, scoped.Hits())
}

func TestFunc_DescribingItFailsSayingSo(t *testing.T) {
	// #42: a Func or Handler reply described nothing, so its description loaded back with the default reply: a Handler
	// that answered 201 came back as an empty 200. Describing it fails now, and says why, wherever the reply is.
	created := Handler(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	fn := Func(func(http.ResponseWriter, *RequestValues) (*Response, error) { return nil, nil })

	for name, reply := range map[string]ReplierFactory{
		"handler":        created,
		"func":           fn,
		"in a sequence":  Seq(OK(), created),
		"after sequence": Seq(OK()).OnSequenceEnded(fn),
		"in a random":    Rand(fn),
	} {
		m := MustNew(Setup().Silent())

		_, err := EncodeMock(m.MustMock(Getf("/h").Reply(reply)).Mocks()[0], "json")
		require.ErrorIs(t, err, errFuncNotDescribed, name)
	}
}
