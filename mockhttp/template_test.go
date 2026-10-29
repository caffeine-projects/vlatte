package mockhttp

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestTemplateData_RequestAndServer(t *testing.T) {
	// Templates read the request's ID, the server's URL, and the server's data as each request arrives.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	ids := make(chan string, 1)
	m.MustMock(Getf("/data").
		Map(func(r *RequestValues, _ *Response) error { ids <- r.ID(); return nil }).
		Reply(OK().BodyTemplate(`{{ .Request.ID }} {{ .Server.URL }} {{ .Data.target }}`)))

	data := map[string]any{"target": "first"}
	m.SetData(data)
	data["target"] = "changed" // the server keeps a copy

	status, body := get(t, m, "/data")
	id := <-ids

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, id+" "+m.URL()+" first", body)
	_, err := uuid.Parse(id)
	require.NoError(t, err)

	m.SetData(map[string]any{"target": "second"})

	_, body = get(t, m, "/data")
	next := <-ids

	require.NotEqual(t, id, next, "each request has its own ID")
	require.Equal(t, next+" "+m.URL()+" second", body)
}

func TestTemplateData_PathParams(t *testing.T) {
	// Templates read the parameters of the mock's path template by name, decoded, as PathParam matches them. A mock
	// without one has none, so reading one fails the request rather than writing nothing.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Request().URLPathTemplate("/files/{dir}/{name}").Reply(OK().BodyTemplate(`{{ .Request.PathParams.dir }} {{ .Request.PathParams.name }}`)))
	m.MustMock(Getf("/plain/*").Reply(OK().BodyTemplate(`{{ .Request.PathParams.name }}`)))

	status, body := get(t, m, "/files/a%2Fb/c%20d.txt")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "a/b c d.txt", body)

	status, header, _ := getFull(t, m, "/plain/x")
	require.NotEqual(t, http.StatusOK, status)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError), "no path template, no parameters: the reply fails")
}

func TestTemplateData_AKeyThatIsNotSetFailsTheRequestAndHasGuardsIt(t *testing.T) {
	// A key that isn't set fails the request, as a missing map key does, so a typo doesn't render as an empty value.
	// Has reads a key that may be missing.
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "set", []byte("v")))
	require.NoError(t, kv.Set(t.Context(), "empty", nil))

	m := MustNew(Setup().Silent().KV(kv))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/get").Reply(OK().BodyTemplate(`[{{ .KV.Get "set" }}][{{ .KV.Get "empty" }}]`)))
	m.MustMock(Getf("/typo").Reply(OK().BodyTemplate(`{{ .KV.Get "sett" }}`)))
	m.MustMock(Getf("/has").Reply(OK().BodyTemplate(`{{ if .KV.Has "set" }}set{{ end }} {{ if .KV.Has "sett" }}sett{{ else }}no sett{{ end }}`)))

	status, body := get(t, m, "/get")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "[v][]", body)

	status, body = get(t, m, "/typo")
	require.Equal(t, StatusNoMatch, status)
	require.Contains(t, body, `key "sett" is not set: set it, or check for it with .KV.Has`)

	status, body = get(t, m, "/has")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, "set no sett", body)
}

func TestServer_SetDataWhileServing(t *testing.T) {
	// Requests read the data while a test sets it, so SetData and Data must be safe for concurrent use.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/data").Reply(OK().BodyTemplate(`{{ index .Data "n" }}`)))

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { m.SetData(map[string]any{"n": i}) })
		wg.Go(func() {
			res, err := http.Get(m.URL("/data"))
			if assert.NoError(t, err) {
				assert.Equal(t, http.StatusOK, res.StatusCode)
				assert.NoError(t, res.Body.Close())
			}
		})
	}

	wg.Wait()
}

// provider is a TemplateDataProvider that gives what its function returns.
type provider struct {
	key  string
	data func(in *TemplateDataProviderInput) (any, error)
}

func (p provider) Key() string { return p.key }

func (p provider) Data(in *TemplateDataProviderInput) (any, error) { return p.data(in) }

func TestTemplateDataProviders(t *testing.T) {
	serve := func(t *testing.T, providers ...TemplateDataProvider) *Server {
		m := MustNew(Setup().Silent().TemplateDataProviders(providers))
		m.MustStart()
		t.Cleanup(m.Close)

		return m
	}

	t.Run("templates read what providers give, by key", func(t *testing.T) {
		m := serve(t, provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) {
			return map[string]any{"name": "ana"}, nil
		}})
		m.MustMock(Getf("/user").Reply(OK().BodyTemplate(`{{ .Provided.user.name }}`)))

		status, body := get(t, m, "/user")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "ana", body)
	})

	t.Run("providers see the mock that matched", func(t *testing.T) {
		m := serve(t, provider{key: "mock", data: func(in *TemplateDataProviderInput) (any, error) {
			return in.Request().Mock.ID(), nil
		}})
		m.MustMock(Getf("/mock").ID("users").Reply(OK().BodyTemplate(`{{ .Provided.mock }}`)))

		_, body := get(t, m, "/mock")
		require.Equal(t, "users", body)
	})

	t.Run("a provider that fails answers with its error", func(t *testing.T) {
		// It used to log the error and answer an empty 200, as if the mock had been served.
		m := serve(t, provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) {
			return nil, errors.New("user service down")
		}})
		m.MustMock(Getf("/user").Reply(OK().BodyTemplate(`{{ .Provided.user }}`)))

		status, header, body := getFull(t, m, "/user")
		require.Equal(t, StatusNoMatch, status)
		require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
		require.Contains(t, body, "http: template data provider user: user service down")
	})

	t.Run("two providers with one key fail the server", func(t *testing.T) {
		// Templates could read only one of them, so this is a setup mistake, not a warning on each request.
		same := provider{key: "user", data: func(*TemplateDataProviderInput) (any, error) { return nil, nil }}

		_, err := New(Setup().Silent().TemplateDataProviders([]TemplateDataProvider{same, same}))
		require.ErrorIs(t, err, vlatte.ErrTemplateDataKeyExists)
		require.ErrorContains(t, err, `"user"`)
	})
}
