package mockhttp

import (
	"bufio"
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"text/template"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
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
	response             Response
	bodyType             bodyType
	bodyEncoding         bodyEncoding
	bodyFilename         string
	bodyFileDir          string // a relative body file opens from here, such as a mock file's dir; "" is the working directory
	teType               teType
	headerWithTemplate   http.Header
	headerTemplate       vlatte.Template
	bodyTemplateContent  string
	bodyFilenameTemplate vlatte.Template
	bodyTemplate         vlatte.Template
	teData               any
	encoded              bool

	delayedErr error
}

type bodyType int

const (
	_bodyDefault bodyType = iota
	_bodyTemplate
	_bodyFile
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
	rep.teType += _teHeader
	return rep
}

// HeaderArrTemplate adds a header with multiple values that can have its value changed using templates.
func (rep *Reply) HeaderArrTemplate(key string, values ...string) *Reply {
	for _, value := range values {
		rep.HeaderTemplate(key, value)
	}
	rep.teType += _teHeader
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
func (rep *Reply) Body(value []byte) *Reply {
	rep.response.Body = value
	return rep
}

// BodyText defines the response body using a string.
func (rep *Reply) BodyText(text string) *Reply {
	rep.response.Body = []byte(text)
	return rep
}

// BodyJSON defines the response body encoding the given value using json.Encoder.
func (rep *Reply) BodyJSON(data any) *Reply {
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
	b, err := io.ReadAll(reader)
	if err != nil {
		rep.delayedErr = err
	}

	rep.response.Body = b

	return rep
}

// BodyFile loads the body content from the given filename.
func (rep *Reply) BodyFile(filename string) *Reply {
	rep.bodyFilename = filename
	rep.bodyType = _bodyFile
	rep.teType += _teBodyFilename

	return rep
}

// BodyTemplate defines the response body using a template.
// The parameter content must be the actual template content that should be parsed and rendered.
// Use BodyFileWithTemplate function to load the template from a file.
func (rep *Reply) BodyTemplate(content string) *Reply {
	rep.bodyTemplateContent = content
	rep.bodyType = _bodyTemplate
	rep.teType += _teBody

	return rep
}

// BodyFileWithTemplate loads the body content from the given filename.
func (rep *Reply) BodyFileWithTemplate(filename string) *Reply {
	rep.bodyFilename = filename
	rep.bodyType = _bodyFile
	rep.teType = _teBodyFilename | _teBody

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

	if len(rep.bodyFilename) > 0 {
		response["body_file"] = rep.bodyFilename
	} else if rep.response.Body != nil {
		response["body"] = string(rep.response.Body)
	}

	switch rep.bodyType {
	case _bodyTemplate:
		tmpl := map[string]any{"enabled": true}

		if rep.teData != nil {
			tmpl["data"] = rep.teData
		}

		response["template"] = tmpl
	}

	return descriptor.New("response", response)
}

func (rep *Reply) beforeBuild(app *Server) error {
	if rep.delayedErr != nil {
		return rep.delayedErr
	}

	if rep.teType&_teBodyFilename == _teBodyFilename {
		var err error
		tmpl := template.New("")
		if tmpl, err = tmpl.Parse(rep.bodyFilename); err != nil {
			return err
		}

		rep.bodyFilenameTemplate = vlatte.TemplateText(tmpl)
	}

	if rep.teType&_teBody == _teBody {
		var err error
		tmpl := template.New("")
		if tmpl, err = tmpl.Parse(rep.bodyTemplateContent); err != nil {
			return err
		}

		rep.bodyTemplate = vlatte.TemplateText(tmpl)
	}

	if len(rep.headerWithTemplate) > 0 {
		buf := bufpool.Get()
		buf.Reset()
		defer bufpool.Put(buf)

		err := rep.headerWithTemplate.Write(buf)
		if err != nil {
			return err
		}

		var tmpl vlatte.Template
		if app.config.Template == nil {
			tmpl = vlatte.TemplateText(template.New(""))
		} else {
			tmpl = app.config.Template
		}

		tmpl, err = tmpl.Parse(buf.String() + "\r\n")
		if err != nil {
			return err
		}

		rep.headerTemplate = tmpl
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

func (rep *Reply) build(_ http.ResponseWriter, r *RequestValues) (res *Response, err error) {
	if rep.delayedErr != nil {
		return nil, rep.delayedErr
	}

	response := rep.response
	hasTemplate := rep.teType&_teBody != 0 || rep.teType&_teBodyFilename != 0 || len(rep.headerWithTemplate) > 0
	tmplData := any(nil)
	if hasTemplate {
		tmplData = r.Data
		if d, ok := r.Data.(*viewdata.Data); ok {
			tmplData = d.WithExtra(rep.teData)
		}
	}

	if len(rep.headerWithTemplate) > 0 {
		buf := bufpool.Get()
		buf.Reset()
		defer bufpool.Put(buf)

		err = rep.headerTemplate.Execute(buf, tmplData)
		if err != nil {
			return nil, err
		}

		tp := textproto.NewReader(bufio.NewReader(buf))
		mimeHeader, err := tp.ReadMIMEHeader()
		if err != nil {
			return nil, err
		}

		// Rendered values belong to this response only, so they go into a copy of the reply headers.
		response.Header = response.Header.Clone()
		for k, values := range mimeHeader {
			for _, v := range values {
				response.Header.Add(k, v)
			}
		}
	}

	switch rep.bodyType {
	case _bodyTemplate:
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf(
					"panic parsing body template. reason=%v",
					recovered,
				)
			}
		}()

		buf := bufpool.Get()
		buf.Reset()

		defer bufpool.Put(buf)

		err = rep.bodyTemplate.Execute(buf, tmplData)
		if err != nil {
			return nil, err
		}

		// The buffer returns to the pool before the response renders, so the body must not share its bytes.
		response.Body = bytes.Clone(buf.Bytes())

	case _bodyFile:
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf(
					"panic loading body from %s. reason=%v",
					rep.bodyFilename,
					recovered,
				)
			}
		}()
		buf := bufpool.Get()
		buf.Reset()

		defer bufpool.Put(buf)

		filename := rep.bodyFilename
		if rep.teType&_teBodyFilename == _teBodyFilename {
			err = rep.bodyFilenameTemplate.Execute(buf, tmplData)
			if err != nil {
				return nil, err
			}

			filename = buf.String()
		}

		if rep.bodyFileDir != "" && !filepath.IsAbs(filename) {
			filename = filepath.Join(rep.bodyFileDir, filename)
		}

		file, err := os.Open(filename)
		if err != nil {
			return nil, err
		}

		if rep.teType&_teBody == _teBody {
			defer file.Close()

			buf.Reset()

			b, err := io.ReadAll(file)
			if err != nil {
				return nil, err
			}

			tmpl := template.New("")
			t, err := tmpl.Parse(string(b))
			if err != nil {
				return nil, err
			}

			err = t.Execute(buf, tmplData)
			if err != nil {
				return nil, err
			}

			response.Body = bytes.Clone(buf.Bytes())
		} else {
			response.BodyCloser = file
		}

		response.BodyFilename = filename
	}

	return &response, nil
}
