package viewdata

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte"
)

// Data is what templates render with: the dot of a Go template, and the context of any other engine.
type Data struct {
	// Request is the request being served.
	Request *Request

	// Provided holds the data of the server's template data providers, by key: {{ .Provided.user }}.
	Provided map[string]any

	// Parameters reads the server's parameters: {{ .Parameters.Get "key" }} and {{ .Parameters.Has "key" }}.
	Parameters *Parameters

	// Server describes the mock server.
	Server *Server

	// Data is the data of the server, set with its SetData: {{ .Data.target }}.
	Data map[string]any

	// Extra is the template data of the reply being rendered: a mock file's template.data. Other templates, such as
	// a webhook's, don't have it.
	Extra any
}

// Server describes the mock server to templates.
type Server struct {
	// URL is the base URL of the server, such as http://127.0.0.1:8080: {{ .Server.URL }}/callback.
	URL string
}

// WithExtra returns a copy of d with the given template data.
func (d *Data) WithExtra(extra any) *Data {
	c := *d
	c.Extra = extra
	return &c
}

// Parameters gives templates the app parameters as text: {{ .Parameters.Get "key" }}. A parameter that may be missing
// reads with Has first: {{ if .Parameters.Has "key" }}.
type Parameters struct {
	ctx context.Context
	kv  vlatte.KV
}

func NewParameters(ctx context.Context, kv vlatte.KV) *Parameters {
	return &Parameters{ctx: ctx, kv: kv}
}

// Get returns the value stored under key as text. A key that isn't stored is an error, as a missing map key is, so a
// typo doesn't render as an empty value.
func (p *Parameters) Get(key string) (string, error) {
	ok, err := p.Has(key)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf("parameters: no parameter %q", key)
	}

	b, err := p.kv.Get(p.ctx, key)

	return string(b), err
}

// Has reports whether a value is stored under key.
func (p *Parameters) Has(key string) (bool, error) {
	return p.kv.Has(p.ctx, key)
}

// Request is the request being served.
type Request struct {
	// ID identifies the request, as the ID of its mockhttp.RequestValues.
	ID     string
	Method string

	// URL is the request URL, on the server's base URL.
	URL *url.URL

	// URLPathSegments are the segments of the URL path: /users/1 is [users 1].
	URLPathSegments []string
	Header          http.Header

	// Cookies holds the cookies of the request, by name.
	Cookies map[string]*http.Cookie

	// Body is the body of the request, or nil when it has none: see HasBody.
	Body *Body

	// PostForm holds the text fields of a form body, urlencoded or multipart: {{ .Request.PostForm.Get "name" }}.
	PostForm url.Values

	// MultipartForm is the multipart form of the body, as http.Request.ParseMultipartForm left it, or nil.
	MultipartForm *multipart.Form
}

// Cookie returns the named cookie, like http.Request.Cookie.
func (r *Request) Cookie(name string) (*http.Cookie, error) {
	if c, ok := r.Cookies[name]; ok {
		return c, nil
	}

	return nil, http.ErrNoCookie
}

// FormFile returns the first file of the field key of a multipart form, as http.Request.FormFile picks it, or nil:
// {{ with .Request.FormFile "avatar" }}{{ .Filename }}{{ end }}.
func (r *Request) FormFile(key string) *multipart.FileHeader {
	if r.MultipartForm == nil {
		return nil
	}

	if files := r.MultipartForm.File[key]; len(files) > 0 {
		return files[0]
	}

	return nil
}

func (r *Request) HasBody() bool {
	return r.Body != nil && len(r.Body.Bytes()) > 0
}

type M interface {
	ID() string
	Name() string
}

type Body struct {
	pc any
	b  []byte
}

func NewBody(kind string, b []byte, parsed any) *Body {
	return &Body{pc: parsed, b: b}
}

func (b *Body) Content() any {
	return b.pc
}

func (b *Body) Bytes() []byte {
	return b.b
}

func (b *Body) Base64() string {
	return base64.StdEncoding.EncodeToString(b.b)
}

func (b *Body) String() string {
	return string(b.b)
}
