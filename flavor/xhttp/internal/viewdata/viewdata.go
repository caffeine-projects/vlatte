package viewdata

import (
	"encoding/base64"
	"net/http"
	"net/url"
)

func New(r *Request, provided map[string]any, extra any, data any) *Data {
	return &Data{Request: r, Provided: provided, Extra: extra, Data: data}
}

// Data is the data used in templates during rendering.
type Data struct {
	Request  *Request
	Provided map[string]any
	Extra    any
	Data     any
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
