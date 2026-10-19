package mockhttp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"uuid"
)

// errFormCut says a multipart form body was cut short, as MaxBodyParsingLimit does.
var errFormCut = errors.New("multipart: the body was cut short: it is shorter than its Content-Length, or has no close delimiter")

func parseRequest(r *http.Request, options *Server) (*RequestValues, []byte, error) {
	parsedURL, urlSegments := parseURL(r, options.URL())
	parsedBody, rawBody, parser, err := parseRequestBody(options, r)
	req := &RequestValues{
		ReqID:           uuid.New().String(),
		StartedAtTime:   time.Now(),
		RawRequest:      r,
		URL:             parsedURL,
		URLPathSegments: urlSegments,
		RawBodyBytes:    rawBody,
		ParsedBodyData:  parsedBody,
		App:             options,
		Mock:            nil,
		Data:            nil,
		bodyParser:      parser,
	}
	return req, rawBody, err
}

// parseURL returns the request URL, on the server's base URL, and its path segments. The path stays escaped as the
// client sent it: decoded, "%3F" and "%23" would start a query or a fragment.
func parseURL(r *http.Request, baseURL string) (*url.URL, []string) {
	// The scheme and host are the server's. A target's user info, opaque part and empty query ("/a?") are left out.
	u := &url.URL{
		Path:        r.URL.Path,
		RawPath:     r.URL.RawPath,
		RawQuery:    r.URL.RawQuery,
		Fragment:    r.URL.Fragment,
		RawFragment: r.URL.RawFragment,
	}
	if base, err := url.Parse(baseURL); err == nil {
		u.Scheme, u.Host = base.Scheme, base.Host
	}

	return u, pathSegments(u)
}

// pathSegments returns the segments of u's path, each decoded: an escaped "/" stays in its segment, so /files/a%2Fb
// is [files a/b].
func pathSegments(u *url.URL) []string {
	segments := strings.FieldsFunc(u.EscapedPath(), func(c rune) bool { return c == '/' })
	for i, s := range segments {
		if d, err := url.PathUnescape(s); err == nil {
			segments[i] = d
		}
	}

	return segments
}

// withFragment moves the fragment of the request target into r.URL.Fragment, before any other handler sees the
// request. Go's server reads no fragment: it leaves a "#" and what follows in r.URL's path or query. So the target is
// parsed again without its fragment, as Go's server parses targets. A CONNECT target is a host, not a URL.
func withFragment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, fragment, ok := strings.Cut(r.RequestURI, "#")
		if !ok || r.Method == http.MethodConnect {
			next.ServeHTTP(w, r)
			return
		}

		u, err := url.ParseRequestURI(target)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		// url.Parse sets Fragment and RawFragment as for any URL. A fragment that doesn't decode is dropped.
		if f, err := url.Parse("#" + fragment); err == nil {
			u.Fragment, u.RawFragment = f.Fragment, f.RawFragment
		}

		// As http.StripPrefix does: a shallow copy with its own URL.
		r2 := new(*r)
		r2.URL = u
		next.ServeHTTP(w, r2)
	})
}

// RequestBodyParser parses the request body if CanParse returns true.
// Multiple implementations of RequestBodyParser can be provided via configuration.
type RequestBodyParser interface {
	// CanParse checks if the current request body should be parsed.
	// The first parameter is the incoming content type.
	CanParse(contentType string, r *http.Request) bool

	// Parse parses the request body.
	Parse(body []byte, r *http.Request) (any, error)
}

// parseRequestBody tests given parsers until it finds one that can parse the request body, and returns it too.
// The user provided RequestBodyParser takes precedence.
func parseRequestBody(app *Server, r *http.Request) (parsedBody any, rawBody []byte, parser RequestBodyParser, err error) {
	if app.config.NoBodyParsing {
		return nil, nil, nil, nil
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return
	}

	if r.Body == nil {
		return
	}

	var reader io.Reader
	if app.config.MaxBodyParsingLimit > 0 {
		reader = io.LimitReader(r.Body, app.config.MaxBodyParsingLimit)
	} else {
		reader = r.Body
	}

	rawBody, err = io.ReadAll(reader)

	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(rawBody))

	if err != nil {
		return nil, nil, nil, err
	}

	if len(rawBody) == 0 {
		return nil, nil, nil, nil
	}

	contentType := r.Header.Get(HeaderContentType)

	for _, parser := range app.requestBodyParsers {
		if parser.CanParse(contentType, r) {
			parsedBody, err = parser.Parse(rawBody, r)
			if err != nil {
				return nil, rawBody, parser,
					fmt.Errorf("%T failed: %w", parser, err)
			}

			return parsedBody, rawBody, parser, nil
		}
	}

	return nil, nil, nil, nil
}

// formURLEncodedParser parses requests whose media type is application/x-www-form-urlencoded.
type formURLEncodedParser struct{}

func (parser *formURLEncodedParser) CanParse(content string, _ *http.Request) bool {
	return mediaType(content) == MIMEFormURLEncoded
}

func (parser *formURLEncodedParser) Parse(body []byte, r *http.Request) (any, error) {
	defer restoreBody(r, body)

	// PostForm is the body's form: Form would add the query.
	err := r.ParseForm()
	return r.PostForm, err
}

// multipartFormParser parses multipart/form-data bodies with http.Request.ParseMultipartForm: the text fields go to
// r.PostForm, as for urlencoded forms, and the files to r.MultipartForm.
type multipartFormParser struct{}

func (parser *multipartFormParser) CanParse(content string, _ *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(content)
	return err == nil && mediaType == MIMEMultipartForm
}

func (parser *multipartFormParser) Parse(body []byte, r *http.Request) (any, error) {
	defer restoreBody(r, body)

	// multipart.Reader.ReadForm takes a body cut right after a delimiter line, or inside the headers of a part, for a
	// whole one, and keeps the parts before the cut. A body MaxBodyParsingLimit cut is shorter than its
	// Content-Length, and only a whole body has its close delimiter: a cut form keeps no field, rather than some.
	_, params, _ := mime.ParseMediaType(r.Header.Get(HeaderContentType))
	if r.ContentLength > int64(len(body)) || !closes(body, params["boundary"]) {
		return nil, errFormCut
	}

	// The body is in memory already, so its files can stay there too: none goes to a temporary file.
	if err := r.ParseMultipartForm(int64(len(body))); err != nil {
		return nil, err
	}

	return r.PostForm, nil
}

// closes reports whether body has the close delimiter of boundary at the start of a line, as a whole multipart body
// has. Within a line, "--boundary--" is content.
func closes(body []byte, boundary string) bool {
	delimiter := []byte("--" + boundary + "--")
	return bytes.HasPrefix(body, delimiter) || bytes.Contains(body, append([]byte("\n"), delimiter...))
}

// restoreBody puts body back into r.Body, which parsing a form reads to the end: proxy, forward and echo replies
// send r.Body.
func restoreBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
}

// plainTextParser parses requests whose media type is text/plain or application/json, as text.
type plainTextParser struct{}

func (parser *plainTextParser) CanParse(content string, _ *http.Request) bool {
	t := mediaType(content)
	return t == MIMETextPlain || t == MIMEApplicationJSON
}

func (parser *plainTextParser) Parse(body []byte, _ *http.Request) (any, error) {
	return string(body), nil
}

// noopParser is the default parser and runs when none is selected.
// It just returns the body []byte.
type noopParser struct{}

func (parser *noopParser) CanParse(_ string, _ *http.Request) bool         { return true }
func (parser *noopParser) Parse(body []byte, _ *http.Request) (any, error) { return body, nil }
