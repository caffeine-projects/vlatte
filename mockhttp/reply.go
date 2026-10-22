package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/textproto"
	"slices"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/bodyfile"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

var (
	_ ReplierFactory = (*Reply)(nil)
	_ Replier        = (*responseReplier)(nil)
)

// errNoReplier is the error of a reply factory that made no Replier, such as Shared(nil).
var errNoReplier = errors.New("reply: the factory made no replier")

// ReplyInput is the input passed to Replier.Reply for HTTP.
type ReplyInput struct {
	RequestValues *RequestValues
	Writer        http.ResponseWriter

	// noSwitch keeps a proxy reply from asking its target to switch protocols, for a mock whose steps or response
	// mappers couldn't apply to the connection: the target answers as to any request.
	noSwitch bool

	// upstream are the steps of the mock that what a proxy reply sends on passes through: the request's body.
	upstream []vlatte.Step
}

// newReplier asks f for the Replier of a mock of app. A factory that makes none fails the build here: the nil Replier
// would panic at the first request.
func newReplier(f ReplierFactory, app *Server) (Replier, error) {
	r, err := f.NewReplier(app)
	if err != nil {
		return nil, err
	}

	if r == nil {
		return nil, errNoReplier
	}

	return r, nil
}

// owned returns what releases r, the Replier f made, when the mock that holds r leaves: r itself when it implements
// io.Closer, and nil when it doesn't, or when f gives the same Replier to every mock, as Shared does.
func owned(f ReplierFactory, r Replier) io.Closer {
	if _, ok := f.(sharedFactory); ok {
		return nil
	}

	c, _ := r.(io.Closer)

	return c
}

// closeAll closes cs, all of them, and returns their errors joined.
func closeAll(cs []io.Closer) error {
	var errs []error
	for _, c := range cs {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// Reply describes the response of a mock: its status, headers, trailers, cookies and body, any of them templates.
// It is a ReplierFactory: each mock built with it gets a Replier of its own, compiled with the template engine of its
// server, so a Reply can serve many mocks and servers, and changing it after Mock changes only the mocks built
// afterwards.
//
// A Reply keeps the values it is given, such as body bytes, template data and a multipart body, without copying them,
// and the mocks built with it read them at each request: don't change them afterwards. Call the methods of the Reply
// instead, which reach the mocks built afterwards.
type Reply struct {
	response            Response
	bodyType            bodyType
	bodyEncoding        bodyEncoding
	bodyFilename        string
	bodyFileDir         string // a relative body file opens from here, such as a mock file's dir; "" is the working directory
	teType              teType
	headerWithTemplate  http.Header
	bodyTemplateContent string
	teData              any
	multipart           *Multipart
	delayedErr          error
}

// headerTemplate is a header value that is a template.
type headerTemplate struct {
	name string
	tmpl vlatte.Template
}

type bodyType int

const (
	_bodyDefault bodyType = iota
	_bodyTemplate
	_bodyFile
	_bodyMultipart
)

type bodyEncoding int

const (
	_bodyEncodingNone bodyEncoding = iota
	_bodyEncodingGZIP
)

type teType byte

const (
	_teBody teType = 1 << iota
	_teBodyFilename
	_teHeader
)

// NewReply creates a new Reply. Prefer to use factory functions for each status code.
func NewReply() *Reply {
	return &Reply{
		response:           *newResponse(),
		bodyType:           _bodyDefault,
		bodyEncoding:       _bodyEncodingNone,
		headerWithTemplate: make(http.Header),
	}
}

// Status creates a new Reply with the given HTTP status code.
func Status(status int) *Reply { return NewReply().Status(status) }

// OK creates a new Reply with http.StatusOK already.
func OK() *Reply { return NewReply().Status(http.StatusOK) }

// Created creates a new Reply with http.StatusCreated already.
func Created() *Reply { return NewReply().Status(http.StatusCreated) }

// Accepted creates a new Reply with http.StatusAccepted already.
func Accepted() *Reply { return NewReply().Status(http.StatusAccepted) }

// NoContent creates a new Reply with http.StatusNoContent already.
func NoContent() *Reply { return NewReply().Status(http.StatusNoContent) }

// PartialContent creates a new Reply with http.StatusPartialContent already.
func PartialContent() *Reply { return NewReply().Status(http.StatusPartialContent) }

// MovedPermanently creates a new Reply with http.StatusMovedPermanently already.
func MovedPermanently(location string) *Reply {
	return NewReply().Status(http.StatusMovedPermanently).Header(HeaderLocation, location)
}

// NotModified creates a new Reply with http.StatusNotModified already.
func NotModified() *Reply { return NewReply().Status(http.StatusNotModified) }

// BadRequest creates a new Reply with http.StatusBadRequest already.
func BadRequest() *Reply { return NewReply().Status(http.StatusBadRequest) }

// Unauthorized creates a new Reply with http.StatusUnauthorized already.
func Unauthorized() *Reply { return NewReply().Status(http.StatusUnauthorized) }

// Forbidden creates a new Reply with http.StatusForbidden already.
func Forbidden() *Reply { return NewReply().Status(http.StatusForbidden) }

// NotFound creates a new Reply with http.StatusNotFound already.
func NotFound() *Reply { return NewReply().Status(http.StatusNotFound) }

// MethodNotAllowed creates a new Reply with http.StatusMethodNotAllowed already.
func MethodNotAllowed() *Reply { return NewReply().Status(http.StatusMethodNotAllowed) }

// UnprocessableEntity creates a new Reply with http.StatusUnprocessableEntity already.
func UnprocessableEntity() *Reply { return NewReply().Status(http.StatusUnprocessableEntity) }

// MultipleChoices creates a new Reply with http.StatusMultipleChoices already.
func MultipleChoices() *Reply { return NewReply().Status(http.StatusMultipleChoices) }

// InternalServerError creates a new Reply with http.StatusInternalServerError already.
func InternalServerError() *Reply { return NewReply().Status(http.StatusInternalServerError) }

// NotImplemented creates a new Reply with http.StatusNotImplemented already.
func NotImplemented() *Reply { return NewReply().Status(http.StatusNotImplemented) }

// BadGateway creates a new Reply with http.StatusBadGateway already.
func BadGateway() *Reply { return NewReply().Status(http.StatusBadGateway) }

// ServiceUnavailable creates a new Reply with http.StatusServiceUnavailable already.
func ServiceUnavailable() *Reply { return NewReply().Status(http.StatusServiceUnavailable) }

// GatewayTimeout creates a new Reply with http.StatusGatewayTimeout already.
func GatewayTimeout() *Reply { return NewReply().Status(http.StatusGatewayTimeout) }

// Status sets the HTTP status code for the MockedResponse.
func (rep *Reply) Status(status int) *Reply {
	rep.response.StatusCode = status
	return rep
}

// Header adds a header to the MockedResponse.
func (rep *Reply) Header(key, value string) *Reply {
	rep.response.Header.Add(key, value)
	return rep
}

// HeaderArr adds a header with multiple values to the MockedResponse.
func (rep *Reply) HeaderArr(key string, values ...string) *Reply {
	for _, value := range values {
		rep.Header(key, value)
	}

	return rep
}

// HeaderTemplate adds a header that can have its value changed using templates.
func (rep *Reply) HeaderTemplate(key, value string) *Reply {
	rep.headerWithTemplate.Add(key, value)
	rep.teType |= _teHeader
	return rep
}

// HeaderArrTemplate adds a header with multiple values that can have its value changed using templates.
func (rep *Reply) HeaderArrTemplate(key string, values ...string) *Reply {
	for _, value := range values {
		rep.HeaderTemplate(key, value)
	}

	return rep
}

// ContentType sets the response content-type misc.Header
func (rep *Reply) ContentType(mime string) *Reply {
	rep.Header(HeaderContentType, mime)
	return rep
}

// Trailer adds a trailer header to the response.
func (rep *Reply) Trailer(key, value string) *Reply {
	rep.response.Trailer.Add(key, value)
	return rep
}

// TrailerArr adds a trailer with multiple values to the response.
func (rep *Reply) TrailerArr(key string, values ...string) *Reply {
	rep.response.Trailer[key] = values
	return rep
}

// Cookie adds a http.Cookie to the response.
func (rep *Reply) Cookie(cookie *http.Cookie) *Reply {
	rep.response.Cookies = append(rep.response.Cookies, cookie)
	return rep
}

// ExpireCookie expires a cookie. It expires a copy, so the given cookie stays as it was.
// TODO: rename this
func (rep *Reply) ExpireCookie(cookie *http.Cookie) *Reply {
	expired := *cookie
	expired.MaxAge = -1
	rep.response.Cookies = append(rep.response.Cookies, &expired)
	return rep
}

// Body defines the response body using a []byte.
// Like every body setter, it replaces the body an earlier one set.
func (rep *Reply) Body(value []byte) *Reply {
	rep.body(_bodyDefault)
	rep.response.Body = value

	return rep
}

// BodyText defines the response body using a string.
func (rep *Reply) BodyText(text string) *Reply {
	return rep.Body([]byte(text))
}

// BodyJSON defines the response body encoding the given value using json.Encoder.
func (rep *Reply) BodyJSON(data any) *Reply {
	rep.body(_bodyDefault)

	b, err := json.Marshal(data, json.Deterministic(true))
	if err != nil {
		rep.delayedErr = err
		return rep
	}

	rep.response.Body = b

	return rep
}

// BodyReader defines the response body using the given io.Reader.
func (rep *Reply) BodyReader(reader io.Reader) *Reply {
	rep.body(_bodyDefault)

	b, err := io.ReadAll(reader)
	if err != nil {
		rep.delayedErr = err
	}

	rep.response.Body = b

	return rep
}

// BodyFile loads the body content from the given filename.
func (rep *Reply) BodyFile(filename string) *Reply {
	rep.body(_bodyFile)
	rep.bodyFilename = filename
	rep.teType |= _teBodyFilename

	return rep
}

// BodyTemplate defines the response body using a template.
// The parameter content must be the actual template content that should be parsed and rendered.
// Use BodyFileWithTemplate function to load the template from a file.
func (rep *Reply) BodyTemplate(content string) *Reply {
	rep.body(_bodyTemplate)
	rep.bodyTemplateContent = content
	rep.teType |= _teBody

	return rep
}

// BodyFileWithTemplate loads the body content from the given filename.
func (rep *Reply) BodyFileWithTemplate(filename string) *Reply {
	rep.body(_bodyFile)
	rep.bodyFilename = filename
	rep.teType |= _teBodyFilename | _teBody

	return rep
}

// Multipart defines the response body as a multipart body. The body sets the Content-Type, with its boundary:
// setting a Content-Type too, with Header or HeaderTemplate, makes building the mock fail.
func (rep *Reply) Multipart(mp *Multipart) *Reply {
	rep.body(_bodyMultipart)
	rep.multipart = mp

	return rep
}

// SetTemplateData sets the data model to be used by templates during response building.
func (rep *Reply) SetTemplateData(data any) *Reply {
	rep.teData = data
	return rep
}

// JSON sets the response to application/json.
func (rep *Reply) JSON(payload any) *Reply {
	rep.Header(HeaderContentType, MIMEApplicationJSON)
	return rep.BodyJSON(payload)
}

// PlainText defines a text/plain response with the given text body.
func (rep *Reply) PlainText(text string) *Reply {
	rep.Header(HeaderContentType, MIMETextPlain)
	return rep.BodyText(text)
}

// Gzip indicates that the response should be gzip encoded.
func (rep *Reply) Gzip() *Reply {
	rep.bodyEncoding = _bodyEncodingGZIP
	return rep
}

func (rep *Reply) Describe() descriptor.Descriptor {
	headers := rep.response.Header.Clone()
	for _, cookie := range rep.response.Cookies {
		headers.Add("Set-Cookie", cookie.String())
	}

	response := map[string]any{"status": rep.response.StatusCode}

	if len(headers) > 0 {
		response["header"] = headers
	}

	if len(rep.response.Trailer) > 0 {
		response["trailers"] = rep.response.Trailer.Clone()
	}

	if rep.bodyEncoding == _bodyEncodingGZIP {
		response["encoding"] = "gzip"
	}

	switch rep.bodyType {
	case _bodyFile:
		response["body_file"] = bodyfile.Path(rep.bodyFileDir, rep.bodyFilename)
	case _bodyTemplate:
		response["body"] = rep.bodyTemplateContent
	case _bodyMultipart:
		spec := rep.multipart.spec
		spec.Dir = rep.bodyFileDir
		response["multipart"] = spec.Doc()
	default:
		if rep.response.Body != nil {
			response["body"] = string(rep.response.Body)
		}
	}

	if len(rep.headerWithTemplate) > 0 {
		response["header_template"] = rep.headerWithTemplate.Clone()
	}

	// template.enabled says the body is a template; template.data is what every template of the reply reads.
	tmpl := make(map[string]any, 2)
	if rep.teType&_teBody != 0 || rep.bodyType == _bodyMultipart && rep.multipart.spec.Templated {
		tmpl["enabled"] = true
	}

	if rep.teData != nil {
		tmpl["data"] = rep.teData
	}

	if len(tmpl) > 0 {
		response["template"] = tmpl
	}

	return descriptor.New("response", response)
}

// NewReplier implements ReplierFactory. It compiles the templates of the reply with the template engine of app into a
// new Replier, and leaves the reply as it was.
func (rep *Reply) NewReplier(app *Server) (Replier, error) {
	if rep.delayedErr != nil {
		return nil, rep.delayedErr
	}

	r := &responseReplier{
		response:     rep.response.clone(),
		bodyType:     rep.bodyType,
		bodyFilename: rep.bodyFilename,
		bodyFileDir:  rep.bodyFileDir,
		teType:       rep.teType,
		teData:       rep.teData,
		templated:    rep.hasTemplates(),
	}

	// Only a reply with templates needs the engine: replies are also made outside a server, in tests.
	if r.templated {
		r.engine = app.config.TemplateEngine
	}

	if rep.bodyType == _bodyMultipart {
		if err := r.compileMultipart(rep); err != nil {
			return nil, err
		}
	}

	if err := r.compileTemplates(rep); err != nil {
		return nil, fmt.Errorf("reply: %w", err)
	}

	if rep.bodyEncoding == _bodyEncodingGZIP {
		// Set, not Add: the reply can have the header already, written with Header.
		r.response.Header.Set(HeaderContentEncoding, "gzip")
		r.response.Encoding = "gzip"
	}

	return r, nil
}

// body sets the kind of body of the reply, and drops what an earlier body setter left: the last one wins.
func (rep *Reply) body(kind bodyType) {
	rep.bodyType = kind
	rep.response.Body = nil
	rep.bodyFilename = ""
	rep.bodyTemplateContent = ""
	rep.teType &^= _teBody | _teBodyFilename
	rep.delayedErr = nil
	rep.multipart = nil
}

// hasTemplates reports whether the reply has templates: a body, a body file name, header values or the text of a
// multipart body.
func (rep *Reply) hasTemplates() bool {
	return rep.teType&(_teBody|_teBodyFilename) != 0 || len(rep.headerWithTemplate) > 0 ||
		rep.bodyType == _bodyMultipart && rep.multipart.spec.Templated
}

// responseReplier serves the response a Reply describes, for one mock. Reply.NewReplier makes it, and requests only
// read it.
type responseReplier struct {
	response             Response // the response of the Reply, with a Header, Trailer and Cookies of its own
	bodyType             bodyType
	bodyFilename         string
	bodyFileDir          string
	teType               teType
	teData               any
	templated            bool                  // responses render with the data of the request
	engine               vlatte.TemplateEngine // compiles the content of a templated body file, read at each reply
	headerTemplates      []headerTemplate
	bodyFilenameTemplate vlatte.Template
	bodyTemplate         vlatte.Template
	multipartEncoder     *mpart.Encoder
}

// Reply implements Replier.
func (r *responseReplier) Reply(_ context.Context, input ReplyInput) (*Response, error) {
	res, err := r.build(input.RequestValues)
	if err != nil {
		return nil, fmt.Errorf("reply: %w", err)
	}

	return res, nil
}

// compileTemplates compiles the body, the body file name and each header value of rep with the engine of r. Header
// values compile one by one, by name, so a value can't spill into another header.
func (r *responseReplier) compileTemplates(rep *Reply) error {
	var err error

	if rep.teType&_teBodyFilename != 0 {
		if r.bodyFilenameTemplate, err = r.engine.Compile("body_file", rep.bodyFilename); err != nil {
			return err
		}
	}

	if rep.bodyType == _bodyTemplate {
		if r.bodyTemplate, err = r.engine.Compile("body", rep.bodyTemplateContent); err != nil {
			return err
		}
	}

	for _, name := range slices.Sorted(maps.Keys(rep.headerWithTemplate)) {
		for _, value := range rep.headerWithTemplate[name] {
			tmpl, err := r.engine.Compile("header."+name, value)
			if err != nil {
				return err
			}

			r.headerTemplates = append(r.headerTemplates, headerTemplate{name: name, tmpl: tmpl})
		}
	}

	return nil
}

// compileMultipart checks the multipart body of rep and compiles it with the engine of r, once per mock: the boundary
// drawn here is the one of every response the mock serves.
func (r *responseReplier) compileMultipart(rep *Reply) error {
	if rep.response.Header.Get(HeaderContentType) != "" || rep.headerWithTemplate.Get(HeaderContentType) != "" {
		return errors.New("reply: a multipart body sets the Content-Type: remove the Content-Type header")
	}

	spec := rep.multipart.spec
	spec.Dir = rep.bodyFileDir

	e, err := mpart.Compile(&spec, r.engine)
	if err != nil {
		return fmt.Errorf("reply: %w", err)
	}

	r.multipartEncoder = e

	return nil
}

func (r *responseReplier) build(rv *RequestValues) (*Response, error) {
	// Each response has a Header, Trailer and Cookies of its own: mappers, callbacks and listeners can change them, and
	// requests run at the same time.
	response := r.response.clone()

	var data any
	if r.templated {
		data = rv.Data
		if d, ok := rv.Data.(*viewdata.Data); ok {
			data = d.WithExtra(r.teData)
		}
	}

	for _, h := range r.headerTemplates {
		value, err := execute(h.tmpl, data)
		if err != nil {
			return nil, err
		}

		// Like net/http when it writes a header, the value goes without the spaces and line breaks around it, such as
		// the line break that ends a YAML block scalar. A line break inside would end the header there, and what follows
		// would read as another one.
		value = textproto.TrimBytes(value)
		if bytes.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("header.%s holds a line break", h.name)
		}

		response.Header.Add(h.name, string(value))
	}

	switch r.bodyType {
	case _bodyMultipart:
		body, pauses, err := r.multipartEncoder.Encode(data)
		if err != nil {
			return nil, err
		}

		response.Header.Set(HeaderContentType, r.multipartEncoder.ContentType())
		response.Body = body
		response.pauses = pauses

	case _bodyTemplate:
		body, err := execute(r.bodyTemplate, data)
		if err != nil {
			return nil, err
		}

		response.Body = body

	case _bodyFile:
		if err := r.openBodyFile(&response, data); err != nil {
			return nil, err
		}
	}

	return &response, nil
}

// openBodyFile sets the body of response to the body file: streamed from the file as it is, or rendered when its
// content is a template. The file name is a template too, and the part of it rendered from data must stay inside
// the directory written before it: see bodyfile.Open.
func (r *responseReplier) openBodyFile(response *Response, data any) error {
	filename := r.bodyFilename
	if r.bodyFilenameTemplate != nil {
		name, err := execute(r.bodyFilenameTemplate, data)
		if err != nil {
			return err
		}

		filename = string(name)
	}

	file, err := bodyfile.Open(r.bodyFileDir, r.bodyFilename, filename)
	if err != nil {
		return err
	}

	response.BodyFilename = file.Name()

	if r.teType&_teBody == 0 {
		response.BodyCloser = file
		return nil
	}

	defer file.Close()

	// The name can change with each request, so the content compiles when it is read.
	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	tmpl, err := r.engine.Compile(filename, string(content))
	if err != nil {
		return err
	}

	response.Body, err = execute(tmpl, data)

	return err
}

// execute renders t with data. The bytes are the caller's: the pooled buffer goes back before the response is
// written, so the bytes can't be the buffer's.
func execute(t vlatte.Template, data any) ([]byte, error) {
	buf := bufpool.Get()
	buf.Reset()

	defer bufpool.Put(buf)

	if err := t.Execute(buf, data); err != nil {
		return nil, err
	}

	return bytes.Clone(buf.Bytes()), nil
}
