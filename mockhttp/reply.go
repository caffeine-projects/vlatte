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

var _ Replier = (*Reply)(nil)

// ReplyInput is the input passed to Replier.Reply for HTTP.
type ReplyInput struct {
	RequestValues *RequestValues
	Writer        http.ResponseWriter
}

type replyOnBeforeBuild interface {
	beforeBuild(app *Server) error
}

// prepareReply runs beforeBuild on replies that need it. Replies holding other replies use it for their children.
func prepareReply(reply Replier, app *Server) error {
	if rb, ok := reply.(replyOnBeforeBuild); ok {
		return rb.beforeBuild(app)
	}

	return nil
}

// Reply holds the configuration on how the MockedResponse should be built.
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
	encoded             bool
	multipart           *Multipart
	multipartEncoder    *mpart.Encoder

	// beforeBuild compiles these with the template engine of the server.
	engine               vlatte.TemplateEngine // compiles the content of a templated body file, read at each reply
	headerTemplates      []headerTemplate
	bodyFilenameTemplate vlatte.Template
	bodyTemplate         vlatte.Template

	delayedErr error
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

// ExpireCookie expires a cookie.
// TODO: rename this
func (rep *Reply) ExpireCookie(cookie *http.Cookie) *Reply {
	cookie.MaxAge = -1
	rep.response.Cookies = append(rep.response.Cookies, cookie)
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

// Reply implements vlatte.Replier[ReplyInput, *Response].
func (rep *Reply) Reply(ctx context.Context, input ReplyInput) (res *Response, err error) {
	res, err = rep.build(input.Writer, input.RequestValues)
	if err != nil {
		return nil, fmt.Errorf("reply: %w", err)
	}
	return res, nil
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

	if len(rep.response.Encoding) > 0 {
		response["encoding"] = rep.response.Encoding
	}

	switch rep.bodyType {
	case _bodyFile:
		response["body_file"] = rep.bodyFilename
	case _bodyTemplate:
		response["body"] = rep.bodyTemplateContent
	case _bodyMultipart:
		response["multipart"] = rep.multipart.spec.Doc()
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

func (rep *Reply) beforeBuild(app *Server) error {
	if rep.delayedErr != nil {
		return rep.delayedErr
	}

	// Only a reply with templates needs the engine: replies are also prepared outside a server, in tests.
	var engine vlatte.TemplateEngine
	if rep.hasTemplates() {
		engine = app.config.TemplateEngine
	}

	if rep.bodyType == _bodyMultipart {
		if err := rep.compileMultipart(engine); err != nil {
			return err
		}
	}

	if err := rep.compileTemplates(engine); err != nil {
		return fmt.Errorf("reply: %w", err)
	}

	if rep.encoded {
		return nil
	}

	switch rep.bodyEncoding {
	case _bodyEncodingGZIP:
		rep.response.Header.Add(HeaderContentEncoding, "gzip")
		rep.response.Encoding = "gzip"
		rep.encoded = true
	}

	return nil
}

// hasTemplates reports whether the reply has templates: a body, a body file name, header values or the text of a
// multipart body.
func (rep *Reply) hasTemplates() bool {
	return rep.teType&(_teBody|_teBodyFilename) != 0 || len(rep.headerWithTemplate) > 0 ||
		rep.bodyType == _bodyMultipart && rep.multipart.spec.Templated
}

// compileTemplates compiles the body, the body file name and each header value with engine, replacing what an
// earlier build compiled. Header values compile one by one, by name, so a value can't spill into another header.
func (rep *Reply) compileTemplates(engine vlatte.TemplateEngine) error {
	rep.engine = engine
	rep.bodyTemplate = nil
	rep.bodyFilenameTemplate = nil
	rep.headerTemplates = nil

	var err error

	if rep.teType&_teBodyFilename != 0 {
		if rep.bodyFilenameTemplate, err = engine.Compile("body_file", rep.bodyFilename); err != nil {
			return err
		}
	}

	if rep.bodyType == _bodyTemplate {
		if rep.bodyTemplate, err = engine.Compile("body", rep.bodyTemplateContent); err != nil {
			return err
		}
	}

	for _, name := range slices.Sorted(maps.Keys(rep.headerWithTemplate)) {
		for _, value := range rep.headerWithTemplate[name] {
			tmpl, err := engine.Compile("header."+name, value)
			if err != nil {
				return err
			}

			rep.headerTemplates = append(rep.headerTemplates, headerTemplate{name: name, tmpl: tmpl})
		}
	}

	return nil
}

// compileMultipart checks the multipart body and compiles it with engine, once per build: the boundary drawn here is
// the one of every response the mock serves.
func (rep *Reply) compileMultipart(engine vlatte.TemplateEngine) error {
	if rep.response.Header.Get(HeaderContentType) != "" || rep.headerWithTemplate.Get(HeaderContentType) != "" {
		return errors.New("reply: a multipart body sets the Content-Type: remove the Content-Type header")
	}

	spec := rep.multipart.spec
	spec.Dir = rep.bodyFileDir

	e, err := mpart.Compile(&spec, engine)
	if err != nil {
		return fmt.Errorf("reply: %w", err)
	}

	rep.multipartEncoder = e

	return nil
}

func (rep *Reply) build(_ http.ResponseWriter, r *RequestValues) (*Response, error) {
	if rep.delayedErr != nil {
		return nil, rep.delayedErr
	}

	response := rep.response

	var data any
	if rep.hasTemplates() {
		data = r.Data
		if d, ok := r.Data.(*viewdata.Data); ok {
			data = d.WithExtra(rep.teData)
		}
	}

	if len(rep.headerTemplates) > 0 {
		// Rendered values belong to this response only, so they go into a copy of the reply headers.
		response.Header = response.Header.Clone()

		for _, h := range rep.headerTemplates {
			value, err := execute(h.tmpl, data)
			if err != nil {
				return nil, err
			}

			// Like net/http when it writes a header, the value goes without the spaces and line breaks around it, such
			// as the line break that ends a YAML block scalar. A line break inside would end the header there, and what
			// follows would read as another one.
			value = textproto.TrimBytes(value)
			if bytes.ContainsAny(value, "\r\n") {
				return nil, fmt.Errorf("header.%s holds a line break", h.name)
			}

			response.Header.Add(h.name, string(value))
		}
	}

	switch rep.bodyType {
	case _bodyMultipart:
		body, pauses, err := rep.multipartEncoder.Encode(data)
		if err != nil {
			return nil, err
		}

		// The reply's headers are shared by its responses, so the Content-Type goes into a copy.
		response.Header = response.Header.Clone()
		response.Header.Set(HeaderContentType, rep.multipartEncoder.ContentType())
		response.Body = body
		response.pauses = pauses

	case _bodyTemplate:
		body, err := execute(rep.bodyTemplate, data)
		if err != nil {
			return nil, err
		}

		response.Body = body

	case _bodyFile:
		if err := rep.openBodyFile(&response, data); err != nil {
			return nil, err
		}
	}

	return &response, nil
}

// openBodyFile sets the body of response to the body file: streamed from the file as it is, or rendered when its
// content is a template. The file name is a template too, and the part of it rendered from data must stay inside
// the directory written before it: see bodyfile.Open.
func (rep *Reply) openBodyFile(response *Response, data any) error {
	filename := rep.bodyFilename
	if rep.bodyFilenameTemplate != nil {
		name, err := execute(rep.bodyFilenameTemplate, data)
		if err != nil {
			return err
		}

		filename = string(name)
	}

	file, err := bodyfile.Open(rep.bodyFileDir, rep.bodyFilename, filename)
	if err != nil {
		return err
	}

	response.BodyFilename = file.Name()

	if rep.teType&_teBody == 0 {
		response.BodyCloser = file
		return nil
	}

	defer file.Close()

	// The name can change with each request, so the content compiles when it is read.
	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	tmpl, err := rep.engine.Compile(filename, string(content))
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
