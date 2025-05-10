package xhttp

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/caffeine-projects/vlatte/drink/xhttp/internal/httprec"
)

var gzipper = &sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// Response defines the HTTP response that will be served once a Mock is matched with an HTTP Request.
type Response struct {
	StatusCode   int
	Header       http.Header
	Trailer      http.Header
	Cookies      []*http.Cookie
	Body         []byte
	BodyCloser   io.ReadCloser
	BodyFilename string
	Encoding     string
}

func newResponse() *Response {
	json.NewEncoder(bytes.NewBuffer(nil)).Encode(&Response{})
	return &Response{Cookies: make([]*http.Cookie, 0), Header: make(http.Header), Trailer: make(http.Header)}
}

// Gunzip decompresses Gzip body.
func (r *Response) Gunzip() ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}

	defer gz.Close()

	return io.ReadAll(gz)
}

func (r *Response) encode(w io.Writer, reader io.Reader) (n int64, err error) {
	switch r.Encoding {
	case "gzip":
		gz := gzipper.Get().(*gzip.Writer)
		gz.Reset(w)

		defer func() {
			gz.Close()
			gzipper.Put(gz)
		}()

		return io.Copy(gz, reader)
	}

	return 0, nil
}

func responseFromWriter(w http.ResponseWriter) (*Response, error) {
	rw := w.(*httprec.HTTPRec)
	result := rw.Result()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}

	defer result.Body.Close()

	if len(w.Header()) != len(result.Header) {
		for k, v := range w.Header() {
			if result.Header.Get(k) == "" {
				for _, vv := range v {
					result.Header.Add(k, vv)
				}
			}
		}
	}

	res := new(Response)
	res.StatusCode = result.StatusCode
	res.Header = result.Header.Clone()
	res.Cookies = result.Cookies()

	if len(body) > 0 {
		res.Body = body
	}

	return res, nil
}
