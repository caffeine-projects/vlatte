package viewdata

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var errKVDown = errors.New("kv down")

func TestRequest_CookiesAndBodyAreAvailableToTemplates(t *testing.T) {
	cookie := &http.Cookie{Name: "session", Value: "abc"}
	r := &Request{Cookies: map[string]*http.Cookie{"session": cookie}, Body: NewBody("text", []byte("tea"), "tea")}

	got, err := r.Cookie("session")
	require.NoError(t, err)
	require.Equal(t, cookie, got)
	_, err = r.Cookie("missing")
	require.ErrorIs(t, err, http.ErrNoCookie)
	require.True(t, r.HasBody())
	require.Equal(t, "tea", r.Body.Content())
	require.Equal(t, []byte("tea"), r.Body.Bytes())
	require.Equal(t, "dGVh", r.Body.Base64())
	require.Equal(t, "tea", r.Body.String())
	require.False(t, (&Request{}).HasBody())
	require.False(t, (&Request{Body: NewBody("text", nil, nil)}).HasBody())
}

func TestRequest_FormFilePicksTheFirstFileOfTheNamedField(t *testing.T) {
	first := &multipart.FileHeader{Filename: "first.txt"}
	second := &multipart.FileHeader{Filename: "second.txt"}
	r := &Request{MultipartForm: &multipart.Form{File: map[string][]*multipart.FileHeader{"doc": {first, second}}}}

	require.Equal(t, first, r.FormFile("doc"))
	require.Nil(t, r.FormFile("missing"))
	require.Nil(t, (&Request{}).FormFile("doc"))
}

// downKV is a key-value store that can't answer.
type downKV struct {
	vlatte.KV
}

func (downKV) Has(context.Context, string) (bool, error) { return false, errKVDown }

func (downKV) Get(context.Context, string) ([]byte, error) { return nil, errKVDown }

func TestKVView_AMissingKeyAndAStoreThatFailsAreErrors(t *testing.T) {
	kv := vlatte.NewMemoryKV()
	require.NoError(t, kv.Set(t.Context(), "empty", nil))

	view := NewKVView(t.Context(), kv)

	t.Run("a stored empty value is empty text", func(t *testing.T) {
		v, err := view.Get("empty")
		require.NoError(t, err)
		require.Empty(t, v)
	})

	t.Run("a key that isn't stored is an error, as a missing map key is", func(t *testing.T) {
		_, err := view.Get("nmae")
		require.EqualError(t, err, `key "nmae" is not set: set it, or check for it with .KV.Has`)

		ok, err := view.Has("nmae")
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("a store that fails fails both", func(t *testing.T) {
		down := NewKVView(t.Context(), downKV{})

		_, err := down.Has("k")
		require.ErrorIs(t, err, errKVDown)

		_, err = down.Get("k")
		require.ErrorIs(t, err, errKVDown)
	})
}
