package xhttp

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"text/template"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/kv"
)

var (
	_ vlatte.TemplateEngine   = (*builtInGoTemplate)(nil)
	_ vlatte.TemplateRenderer = (*builtInGoTemplateRender)(nil)
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

// builtInGoTemplate is the built-in TemplateEngine implementation.
// It uses Go templates.
type builtInGoTemplate struct {
	funcMap template.FuncMap
}

func newGoTemplate() *builtInGoTemplate {
	return &builtInGoTemplate{funcMap: make(template.FuncMap)}
}

func (gt *builtInGoTemplate) Load() error {
	return nil
}

// FuncMap adds a new function to be used inside the Go template.
func (gt *builtInGoTemplate) FuncMap(fn template.FuncMap) *builtInGoTemplate {
	gt.funcMap = fn
	return gt
}

func (gt *builtInGoTemplate) Parse(s string) (vlatte.TemplateRenderer, error) {
	t, err := template.New("").Funcs(gt.funcMap).Parse(s)
	if err != nil {
		return nil, err
	}

	return newGoTemplateRender(t), nil
}

type builtInGoTemplateRender struct {
	template *template.Template
}

func newGoTemplateRender(template *template.Template) vlatte.TemplateRenderer {
	return &builtInGoTemplateRender{template: template}
}

func (gt *builtInGoTemplateRender) Render(w io.Writer, data any) error {
	return gt.template.Execute(w, data)
}
