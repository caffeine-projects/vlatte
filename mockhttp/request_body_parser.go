package mockhttp

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"uuid"
)

func parseRequest(r *http.Request, options *Server) (*RequestValues, []byte, error) {
	parsedURL, urlSegments := parseURL(r, options.URL())
	parsedBody, rawBody, err := parseRequestBody(options, r)
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
	}
	return req, rawBody, err
}

// parseURL returns the full request URL (with base) and path segments.
func parseURL(r *http.Request, baseURL string) (*url.URL, []string) {
	reqPath := r.URL.Path
	if len(r.URL.RawQuery) > 0 {
		reqPath += "?" + r.URL.RawQuery
	}
	parsedURL, _ := url.Parse(baseURL + reqPath)
	segments := strings.FieldsFunc(r.URL.Path, func(c rune) bool { return c == '/' })
	return parsedURL, segments
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

// parseRequestBody tests given parsers until it finds one that can parse the request body.
// The user provided RequestBodyParser takes precedence.
func parseRequestBody(app *Server, r *http.Request) (parsedBody any, rawBody []byte, err error) {
	if app.config.NoBodyParsing {
		return nil, nil, nil
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
		return nil, nil, err
	}

	if len(rawBody) == 0 {
		return nil, nil, nil
	}

	contentType := r.Header.Get(HeaderContentType)

	for _, parser := range app.requestBodyParsers {
		if parser.CanParse(contentType, r) {
			parsedBody, err = parser.Parse(rawBody, r)
			if err != nil {
				return nil, rawBody,
					fmt.Errorf("%T failed: %w", parser, err)
			}

			return parsedBody, rawBody, nil
		}
	}

	return nil, nil, nil
}

// jsonBodyParser parses requests with content type header containing "application/x-www-form-urlencoded"
type formURLEncodedParser struct{}

func (parser *formURLEncodedParser) CanParse(content string, _ *http.Request) bool {
	return strings.Contains(content, MIMEFormURLEncoded)
}

func (parser *formURLEncodedParser) Parse(_ []byte, r *http.Request) (any, error) {
	err := r.ParseForm()
	return r.Form, err
}

// plainTextParser parses requests with content type header containing "text/plain"
type plainTextParser struct{}

func (parser *plainTextParser) CanParse(content string, _ *http.Request) bool {
	return strings.Contains(content, MIMETextPlain) || strings.Contains(content, MIMEApplicationJSON)
}

func (parser *plainTextParser) Parse(body []byte, _ *http.Request) (any, error) {
	return string(body), nil
}

// noopParser is the default parser and runs when none is selected.
// It just returns the body []byte.
type noopParser struct{}

func (parser *noopParser) CanParse(_ string, _ *http.Request) bool         { return true }
func (parser *noopParser) Parse(body []byte, _ *http.Request) (any, error) { return body, nil }
