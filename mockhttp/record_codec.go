package mockhttp

import (
	"bytes"
	"cmp"
	"compress/gzip"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var _ vlatte.RecordCodec[*recordedCall, RecordedRequest, RecordedResponse] = (*recordCodec)(nil)

// _segmentEscaper escapes what a path segment can hold that a path escaped as a whole doesn't: "/", and "*", a glob
// in a mock file.
var _segmentEscaper = strings.NewReplacer("/", "%2F", "*", "%2A")

// errQueryNotUTF8 refuses a call whose query can't be written in a mock file.
var errQueryNotUTF8 = errors.New("recorder: the query doesn't parse, and isn't UTF-8 text")

// _bodyExtensions name the body files of a recording after the media type of their content, the same way on every
// system. Other media types end in .bin.
var _bodyExtensions = map[string]string{
	"application/javascript": ".js",
	"application/json":       ".json",
	"application/pdf":        ".pdf",
	"application/x-yaml":     ".yaml",
	"application/xml":        ".xml",
	"application/yaml":       ".yaml",
	"application/zip":        ".zip",
	"image/gif":              ".gif",
	"image/jpeg":             ".jpg",
	"image/png":              ".png",
	"image/svg+xml":          ".svg",
	"image/webp":             ".webp",
	"text/css":               ".css",
	"text/csv":               ".csv",
	"text/html":              ".html",
	"text/javascript":        ".js",
	"text/plain":             ".txt",
	"text/xml":               ".xml",
	"text/yaml":              ".yaml",
}

// _sentBytesHeaders hold for the bytes of a body as sent: a gzip body decoded, then compressed again when it is
// served, would no longer match them. A strong ETag does too; a weak one holds for any encoding of the content.
var _sentBytesHeaders = []string{"Content-MD5", "Digest", "Content-Digest", "Repr-Digest", "Content-Range"}

// MatcherSpec is a matcher as mock files write it, such as ["eqj", "{...}"] or ["not", "present"].
type MatcherSpec []any

// RecordedRequest is what a recording matches: the request section of its mock file.
type RecordedRequest struct {
	Method string `json:"method"`

	// URL is the path and query, which must be the same: the parameters in any order, each with its values in order.
	URL string `json:"url"`

	// Header matches the request headers RecordConfig.RequestHeaders names, and the media type of a body.
	Header map[string]MatcherSpec `json:"header,omitempty"`

	// FormFile matches the files of a multipart form, by field name.
	FormFile map[string][]RecordedFile `json:"form_file,omitempty"`

	// Body matches the body. A recording matches no body it couldn't read, such as one that isn't text.
	Body MatcherSpec `json:"body,omitempty"`
}

// RecordedFile is a file of a recorded multipart form: a form_file pattern.
type RecordedFile struct {
	Filename    string      `json:"filename"`
	ContentType string      `json:"content_type,omitempty"`
	Body        MatcherSpec `json:"body,omitempty"`
	BodyFile    string      `json:"body_file,omitempty"`
}

// RecordedResponse is a response a recording replies with: a response section of its mock file.
type RecordedResponse struct {
	Status  int                 `json:"status"`
	Header  map[string][]string `json:"header,omitempty"`
	Trailer map[string][]string `json:"trailer,omitempty"`

	// Body is a text body, written inline. A body that isn't text, or is larger than RecordConfig.MaxInlineBody, goes
	// to BodyFile, next to the mock file.
	Body     string `json:"body,omitempty"`
	BodyFile string `json:"body_file,omitempty"`

	// Encoding is gzip for a gzip body recorded decoded: it is compressed again when it is served.
	Encoding string `json:"encoding,omitempty"`
}

// recordedMockFile is the mock file of a recording.
type recordedMockFile struct {
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	Request  RecordedRequest   `json:"request"`
	Response *RecordedResponse `json:"response,omitzero"`
	Sequence *recordedSequence `json:"response_sequence,omitzero"`
}

type recordedSequence struct {
	Responses  []RecordedResponse `json:"responses"`
	AfterEnded RecordedResponse   `json:"after_ended"`
}

// recordCodec reads the calls the server captured as mocks.
type recordCodec struct {
	headers   []string            // the request headers recordings match, canonical
	skip      map[string]struct{} // the response headers recordings leave out, canonical
	redact    map[string]struct{} // the headers whose values recordings hide, in lower case, as Config holds them
	maxInline int64
	logger    *zerolog.Logger
}

// newRecordCodec returns the codec of a recording with conf, which hides the values of the headers redact names, as
// the request log does.
func newRecordCodec(conf *RecordConfig, redact map[string]struct{}, logger *zerolog.Logger) *recordCodec {
	c := &recordCodec{
		skip:      make(map[string]struct{}, len(_forbiddenHeaders)+2+len(_debugHeaders)+len(conf.ExcludeResponseHeaders)),
		redact:    redact,
		maxInline: cmp.Or(conf.MaxInlineBody, _defaultMaxInlineBody),
		logger:    logger,
	}

	for _, name := range conf.RequestHeaders {
		if name = http.CanonicalHeaderKey(name); !slices.Contains(c.headers, name) {
			c.headers = append(c.headers, name)
		}
	}

	// The server measures and dates each response it sends, and tells how it served it in the debug headers.
	skipped := [][]string{_forbiddenHeaders, {HeaderContentLength, "Date"}, _debugHeaders, conf.ExcludeResponseHeaders}
	for _, names := range skipped {
		for _, name := range names {
			c.skip[http.CanonicalHeaderKey(name)] = struct{}{}
		}
	}

	return c
}

// Name names the mock of x: its method and URL, such as "GET /items?page=2".
func (c *recordCodec) Name(x *recordedCall) string {
	target, err := x.target()
	if err != nil {
		target = x.url.EscapedPath()
	}

	return strings.ToUpper(x.method) + " " + target
}

// Request writes what the recording of x matches: its method, URL, the headers the recording names, and its body.
func (c *recordCodec) Request(x *recordedCall, files *vlatte.RecordFiles) (RecordedRequest, error) {
	target, err := x.target()
	if err != nil {
		return RecordedRequest{}, err
	}

	// Methods match ignoring case, so calls that differ in case are one request.
	q := RecordedRequest{Method: strings.ToUpper(x.method), URL: target, Header: make(map[string]MatcherSpec, len(c.headers))}

	body, formFiles, skipped := c.body(x, files)
	if skipped != "" {
		c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: the recording matches any body: %s", skipped)
	}

	q.Body, q.FormFile = body, formFiles

	for _, name := range c.headers {
		v := x.header.Get(name)

		switch {
		case v == "":
			// Header.Get reads an empty header as a missing one.
			q.Header[name] = MatcherSpec{"not", "present"}
		case c.redacted(name):
			// Its value is a secret, such as a token: the recording matches that it came, not what it was.
			q.Header[name] = MatcherSpec{"present"}
		case !utf8.ValidString(v):
			c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: the recording matches any %s: its value isn't UTF-8 text", name)
		case name == HeaderContentType:
			// Its parameters, such as the boundary of a form, change with each client: the media type stays.
			if spec := mediaTypeMatcher(v); spec != nil {
				q.Header[name] = spec
			}
		default:
			q.Header[name] = MatcherSpec{"equal", v}
		}
	}

	// The type of a body is how the server reads it: the body matches for its media type only.
	if len(q.Body) > 0 && q.Body[0] != "empty" {
		delete(q.Header, HeaderContentType)

		if contentType := x.header.Get(HeaderContentType); contentType == "" {
			q.Header[HeaderContentType] = MatcherSpec{"not", "present"}
		} else if spec := mediaTypeMatcher(contentType); spec != nil {
			q.Header[HeaderContentType] = spec
		}
	}

	return q, nil
}

// mediaTypeMatcher returns the matcher of the media type of a Content-Type, whatever its parameters, as
// Builder.ContentType makes it, or nil when it isn't UTF-8 text.
func mediaTypeMatcher(contentType string) MatcherSpec {
	// Before ToLower, which writes what isn't UTF-8 as U+FFFD.
	media, _, _ := strings.Cut(contentType, ";")
	if !utf8.ValidString(media) {
		return nil
	}

	media = strings.ToLower(strings.TrimSpace(media))

	return MatcherSpec{"split", ";", []any{"item", 0, []any{"trim", []any{"eqi", media}}}}
}

// Response writes what x answered: its status, headers, trailers and body, as the client got them. A gzip body that
// stayed in memory is recorded decoded, with encoding gzip, so it reads in the mock file, unless a header holds for
// its bytes as sent, such as a strong ETag or a digest. Other encoded bodies, and bodies that went to a file while
// they were captured, are kept as they were sent, with their Content-Encoding.
func (c *recordCodec) Response(x *recordedCall, files *vlatte.RecordFiles) (RecordedResponse, error) {
	if err := x.resBody.Err(); err != nil {
		return RecordedResponse{}, err
	}

	if x.status < 100 || x.status > 599 {
		return RecordedResponse{}, fmt.Errorf("recorder: status %d can't be in a mock file: it takes 100 to 599", x.status)
	}

	s := RecordedResponse{Status: x.status, Header: c.header(x, x.resHeader), Trailer: c.header(x, x.trailer)}

	encoding := strings.ToLower(strings.TrimSpace(x.resHeader.Get(HeaderContentEncoding)))
	if encoding == "identity" {
		encoding = ""
	}

	ext := bodyExt(x.resHeader.Get(HeaderContentType))

	body, inMemory := x.resBody.Bytes()
	if !inMemory {
		if encoding != "" {
			ext = ".bin"
		}

		name, err := files.PlaceSpooled(x.resBody, ext)
		if err != nil {
			return RecordedResponse{}, err
		}

		s.BodyFile = name

		return s, nil
	}

	if len(body) == 0 {
		return s, nil
	}

	if encoding == "gzip" && !holdsSentBytes(s) {
		if decoded, ok := gunzip(body, c.maxInline); ok {
			body, encoding, s.Encoding = decoded, "", "gzip"

			// The encoding sets it when the mock replies.
			delete(s.Header, HeaderContentEncoding)
		}
	}

	if encoding != "" {
		ext = ".bin"
	}

	name, inline := files.Place(body, encoding == "" && utf8.Valid(body), ext)
	if inline {
		s.Body = string(body)
	} else {
		s.BodyFile = name
	}

	return s, nil
}

// Mock writes the mock file of m: its response, or a sequence of its responses whose last one repeats.
func (c *recordCodec) Mock(m *RecordedMock) any {
	doc := recordedMockFile{ID: m.ID, Name: m.Name, Request: m.Request}

	if len(m.Responses) == 1 {
		doc.Response = &m.Responses[0]
	} else {
		doc.Sequence = &recordedSequence{Responses: m.Responses, AfterEnded: m.Responses[len(m.Responses)-1]}
	}

	return doc
}

// body returns the body matcher of x's request and the patterns of its form files. A body it can't match returns
// nil, and why when it is worth saying.
func (c *recordCodec) body(x *recordedCall, files *vlatte.RecordFiles) (MatcherSpec, map[string][]RecordedFile, string) {
	switch x.bodyMode {
	case bodyUnmatched:
		return nil, nil, x.bodySkip
	case bodyEmpty:
		return MatcherSpec{"empty"}, nil, ""
	}

	// The server reads a body by its media type, with the parsers it comes with: a form as its values, and any
	// other body as text.
	contentType := x.header.Get(HeaderContentType)
	media := mediaType(contentType)

	switch {
	case media == MIMEMultipartForm:
		return c.multipart(x, contentType, files)
	case media == MIMEFormURLEncoded:
		// As http.Request.ParseForm does.
		if x.method != http.MethodPost && x.method != http.MethodPut && x.method != http.MethodPatch {
			return nil, nil, "the server reads no form from a " + x.method + " request"
		}

		form, err := url.ParseQuery(string(x.body))
		if err != nil {
			return nil, nil, "the form doesn't parse"
		}

		if !utf8Values(form) {
			return nil, nil, "the form isn't UTF-8 text"
		}

		return MatcherSpec{"equal", form}, nil, ""
	case !utf8.Valid(x.body):
		return nil, nil, "the body isn't UTF-8 text"
	case media == MIMEApplicationJSON || strings.HasSuffix(media, "+json"):
		// JSON written one way, so equal documents are one request. JSON this can't read, such as one with a
		// name twice, matches as the text it is.
		if doc, err := value.DecodeJSON(x.body); err == nil {
			if canonical, err := json.Marshal(doc, json.Deterministic(true)); err == nil {
				return MatcherSpec{"eqj", string(canonical)}, nil, ""
			}
		}

		return MatcherSpec{"equal", string(x.body)}, nil, ""
	default:
		return MatcherSpec{"equal", string(x.body)}, nil, ""
	}
}

// multipart returns the matcher of a multipart form's text fields, as the server reads them, and the patterns of its
// files. A file that isn't text goes to a body file.
func (c *recordCodec) multipart(x *recordedCall, contentType string, files *vlatte.RecordFiles) (MatcherSpec, map[string][]RecordedFile, string) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, nil, "the form has no boundary"
	}

	// In memory, as the server parses it.
	form, err := multipart.NewReader(bytes.NewReader(x.body), params["boundary"]).ReadForm(int64(len(x.body)))
	if err != nil {
		return nil, nil, "the form doesn't parse"
	}

	defer form.RemoveAll()

	values := url.Values(form.Value)
	if !utf8Values(values) {
		return nil, nil, "the form isn't UTF-8 text"
	}

	var patterns map[string][]RecordedFile

	for field, headers := range form.File {
		for _, fh := range headers {
			if !utf8.ValidString(field) || !utf8.ValidString(fh.Filename) {
				c.logger.Warn().Str("call", c.Name(x)).Msg("recorder: a form file whose name isn't UTF-8 text isn't matched")
				continue
			}

			content, err := fileContent(fh)
			if err != nil {
				return nil, nil, "a file of the form can't be read"
			}

			file := RecordedFile{Filename: fh.Filename}
			if media := mediaType(fh.Header.Get(HeaderContentType)); utf8.ValidString(media) {
				file.ContentType = media
			}

			name, inline := files.Place([]byte(content), utf8.ValidString(content), bodyExt(fh.Header.Get(HeaderContentType)))
			if inline {
				file.Body = MatcherSpec{"equal", content}
			} else {
				file.BodyFile = name
			}

			if patterns == nil {
				patterns = make(map[string][]RecordedFile, len(form.File))
			}

			patterns[field] = append(patterns[field], file)
		}
	}

	return MatcherSpec{"equal", values}, patterns, ""
}

// header returns the headers of x's response that a recording keeps, under their canonical names: those the server
// doesn't set, and the recording doesn't leave out. The Content-Length of a HEAD answer, which has no body for the
// server to measure, is kept. A value that isn't UTF-8 text is left out, and a redacted header has one value that
// stands for its values.
func (c *recordCodec) header(x *recordedCall, h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))

	for k, values := range h {
		name := http.CanonicalHeaderKey(k)
		if _, skip := c.skip[name]; skip && (!strings.EqualFold(name, HeaderContentLength) || x.method != http.MethodHead) {
			continue
		}

		if c.redacted(name) {
			if len(values) > 0 {
				out[name] = []string{_redacted}
			}

			continue
		}

		for _, v := range values {
			if !utf8.ValidString(v) {
				c.logger.Warn().Str("call", c.Name(x)).Msgf("recorder: a value of %s isn't UTF-8 text: it isn't recorded", name)
				continue
			}

			out[name] = append(out[name], v)
		}
	}

	return out
}

// redacted reports whether recordings hide the values of the header name.
func (c *recordCodec) redacted(name string) bool {
	_, ok := c.redact[strings.ToLower(name)]
	return ok
}

// canonicalURL writes the path and query of u one way, so requests a recording can't tell apart are recorded alike.
// Each path segment is decoded, then escaped as a path escapes it, with a "/" in a segment as %2F, and a "*" as %2A, so
// a mock file reads it as no glob. The query is sorted by name, each name with its values in order. A query that
// doesn't parse is kept as it came, which is how match.RequestURI compares it.
func canonicalURL(u *url.URL) (string, error) {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}

	segments := strings.Split(path, "/")
	for i, s := range segments {
		if decoded, err := url.PathUnescape(s); err == nil {
			segments[i] = _segmentEscaper.Replace((&url.URL{Path: decoded}).EscapedPath())
		}
	}

	target := strings.Join(segments, "/")
	if u.RawQuery == "" {
		return target, nil
	}

	query, err := url.ParseQuery(u.RawQuery)
	switch {
	case err == nil && len(query) == 0:
		return target, nil
	case err == nil:
		return target + "?" + query.Encode(), nil
	case !utf8.ValidString(u.RawQuery):
		return "", errQueryNotUTF8
	default:
		return target + "?" + u.RawQuery, nil
	}
}

// holdsSentBytes reports whether a header or trailer of s holds for the bytes of its body as sent: a strong ETag, a
// digest of them, or the range they are of.
func holdsSentBytes(s RecordedResponse) bool {
	for _, h := range []http.Header{s.Header, s.Trailer} {
		if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
			return true
		}

		for _, name := range _sentBytesHeaders {
			if h.Get(name) != "" {
				return true
			}
		}
	}

	return false
}

// gunzip decodes a gzip body that decodes whole within limit bytes.
func gunzip(body []byte, limit int64) ([]byte, bool) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}

	defer gz.Close()

	// One byte more tells a body that doesn't fit.
	read := limit
	if read < math.MaxInt64 {
		read++
	}

	decoded, err := io.ReadAll(io.LimitReader(gz, read))
	if err != nil || int64(len(decoded)) > limit {
		return nil, false
	}

	return decoded, true
}

// bodyExt returns the extension of the body file of a body with that Content-Type.
func bodyExt(contentType string) string {
	media := mediaType(contentType)
	if ext, ok := _bodyExtensions[media]; ok {
		return ext
	}

	switch {
	case strings.HasSuffix(media, "+json"):
		return ".json"
	case strings.HasSuffix(media, "+xml"):
		return ".xml"
	default:
		return ".bin"
	}
}

// utf8Values reports whether the names and values of a form are UTF-8 text, as a mock file holds them.
func utf8Values(values url.Values) bool {
	for k, vv := range values {
		if !utf8.ValidString(k) {
			return false
		}

		for _, v := range vv {
			if !utf8.ValidString(v) {
				return false
			}
		}
	}

	return true
}
