package viewdata

import (
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/params"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
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

	// Scenarios reads the state of the server's scenarios: {{ .Scenarios.State "checkout" }}, and of the instance of
	// a key, {{ .Scenarios.StateOf "checkout" (.Request.Header.Get "X-Test-Id") }}. A scenario the mock that matched
	// moved is in the state it moved it to, even once another request moved it on.
	Scenarios matchscenario.View
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

// Parameters gives templates the app parameters as text: {{ .Parameters.Get "key" }}.
type Parameters = params.Parameters

// NewParameters returns the Parameters of kv, read with ctx.
func NewParameters(ctx context.Context, kv vlatte.KV) *Parameters {
	return params.New(ctx, kv)
}

// Request is the request being served.
type Request struct {
	// ID identifies the request, as the ID of its mockhttp.RequestValues.
	ID     string
	Method string

	// URL is the request URL: the one a request in absolute form, as a forward proxy gets, or an intercepted one names,
	// and otherwise on the server's base URL.
	URL *url.URL

	// URLPathSegments are the segments of the URL path, each decoded: /users/1 is [users 1], and /files/a%2Fb is
	// [files a/b].
	URLPathSegments []string

	// PathParams holds the parameters of the path template of the mock that matched, decoded, by name: /users/{id}
	// gives {{ .Request.PathParams.id }}. A mock without one has none.
	PathParams map[string]string

	Header http.Header

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
