package xhttp

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte/shelf/kv"
)

// templateData is the data used in templates during rendering.
type templateData struct {
	Request *templateRequest
	Extra   any

	params  kv.KV
	data    map[string]any
	baseURL string
}

func (t *templateData) Parameters() kv.KV {
	return t.params
}

func (t *templateData) Data() map[string]any {
	return t.data
}

func (t *templateData) URL(paths ...string) string {
	if len(paths) == 0 {
		return t.baseURL
	}

	u, err := url.JoinPath(t.baseURL, paths...)
	if err != nil {
		return ""
	}

	return u
}

type templateRequest struct {
	Method          string
	URL             *url.URL
	URLPathSegments []string
	Header          http.Header
	Cookies         []*http.Cookie
	Body            any
}

func (t *templateRequest) Cookie(name string) (*http.Cookie, error) {
	for _, cookie := range t.Cookies {
		if cookie.Name == name {
			return cookie, nil
		}
	}

	return nil, fmt.Errorf(`cookie "%s" not found`, name)
}
