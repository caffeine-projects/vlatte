package viewdata

import (
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte"
)

func New(r *Request, provided map[string]any, params *Parameters) *Data {
	return &Data{Request: r, Provided: provided, Parameters: params}
}

// Data is the data used in templates during rendering.
type Data struct {
	Request    *Request
	Provided   map[string]any
	Parameters *Parameters
	// Extra is the template data of the reply being rendered.
	Extra any
	Data  any
}

// WithExtra returns a copy of d with the given template data.
func (d *Data) WithExtra(extra any) *Data {
	c := *d
	c.Extra = extra
	return &c
}

// Parameters gives templates the app parameters as text: {{ .Parameters.Get "key" }}.
type Parameters struct {
	ctx context.Context
	kv  vlatte.KV
}

func NewParameters(ctx context.Context, kv vlatte.KV) *Parameters {
	return &Parameters{ctx: ctx, kv: kv}
}

// Get returns the value stored under key as text.
func (p *Parameters) Get(key string) (string, error) {
	b, err := p.kv.Get(p.ctx, key)
	return string(b), err
}

type Request struct {
	ID              string
	Method          string
	URL             *url.URL
	URLPathSegments []string
	Header          http.Header
	Cookies         map[string]*http.Cookie
	Body            *Body

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
