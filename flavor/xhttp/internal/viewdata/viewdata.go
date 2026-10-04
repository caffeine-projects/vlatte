package viewdata

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"

	"github.com/caffeine-projects/vlatte/vlattecore/kv"
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
	kv  kv.KV
}

func NewParameters(ctx context.Context, kv kv.KV) *Parameters {
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
}

// Cookie returns the named cookie, like http.Request.Cookie.
func (r *Request) Cookie(name string) (*http.Cookie, error) {
	if c, ok := r.Cookies[name]; ok {
		return c, nil
	}

	return nil, http.ErrNoCookie
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
