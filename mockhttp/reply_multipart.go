package mockhttp

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"slices"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mpart"
)

var _ vlatte.SelfDescriptive = (*Multipart)(nil)

// Multipart is a multipart body, for a reply or a webhook: its subtype, boundary and parts, written with
// mime/multipart.Writer. Start one with MultipartForm, MultipartMixed or NewMultipart.
type Multipart struct {
	spec mpart.Spec
}

// MultipartForm starts a multipart/form-data body. Each of its parts needs a name.
func MultipartForm(parts ...*Part) *Multipart {
	return NewMultipart("form-data", parts...)
}

// MultipartMixed starts a multipart/mixed body.
func MultipartMixed(parts ...*Part) *Multipart {
	return NewMultipart("mixed", parts...)
}

// NewMultipart starts a body of multipart/<subtype>, such as related, alternative, byteranges or x-mixed-replace.
// The parts are copied: changing one afterwards doesn't change the body.
func NewMultipart(subtype string, parts ...*Part) *Multipart {
	mp := &Multipart{spec: mpart.Spec{Subtype: subtype, Parts: make([]*mpart.PartSpec, len(parts))}}
	for i, p := range parts {
		spec := p.spec
		spec.Header = slices.Clone(p.spec.Header)
		spec.Body = bytes.Clone(p.spec.Body)
		mp.spec.Parts[i] = &spec
	}

	return mp
}

// Boundary sets the boundary between the parts. Without one, a random boundary is drawn when the mock is built.
func (mp *Multipart) Boundary(boundary string) *Multipart {
	mp.spec.Boundary = boundary
	return mp
}

// Param adds a parameter to the Content-Type, such as type and start for multipart/related.
func (mp *Multipart) Param(name, value string) *Multipart {
	if mp.spec.Params == nil {
		mp.spec.Params = make(map[string]string, 1)
	}

	mp.spec.Params[name] = value

	return mp
}

// Template makes the text of the parts templates, compiled with the template engine of the server and rendered with
// the data of each reply: their names, filenames, content types, header values, text bodies and body file names.
// Byte and JSON bodies, and the content of body files, are written as they are.
func (mp *Multipart) Template() *Multipart {
	mp.spec.Templated = true
	return mp
}

// Describe implements vlatte.SelfDescriptive, as the parameters of a webhook write the body: under multipart, or
// multipart_template when its text is templates. A reply describes its body itself.
func (mp *Multipart) Describe() vlatte.Descriptor {
	if mp.spec.Templated {
		return vlatte.NewDescriptor("multipart_template", mp.spec.Doc())
	}

	return vlatte.NewDescriptor("multipart", mp.spec.Doc())
}

// Check checks the body and compiles its templates with engine, as Encode does, without writing it: a webhook checks
// its body when its mock is built.
func (mp *Multipart) Check(engine vlatte.TemplateEngine) error {
	_, err := mp.compile(engine)
	return err
}

// Encode writes the whole body, with its templates compiled with engine and rendered with data, under a boundary
// drawn now when none is set. It returns the body's Content-Type and bytes. engine can be nil when the body isn't
// templated. A body written at once can't pause, so a part with a delay is an error: delays apply to replies only.
func (mp *Multipart) Encode(engine vlatte.TemplateEngine, data any) (contentType string, body []byte, err error) {
	e, err := mp.compile(engine)
	if err != nil {
		return "", nil, err
	}

	body, _, err = e.Encode(data)

	return e.ContentType(), body, err
}

// compile compiles the body with engine for writing it at once.
func (mp *Multipart) compile(engine vlatte.TemplateEngine) (*mpart.Encoder, error) {
	if i := slices.IndexFunc(mp.spec.Parts, func(p *mpart.PartSpec) bool { return p.Delay > 0 }); i >= 0 {
		return nil, fmt.Errorf("multipart: parts[%d].delay can't be set: a body written at once doesn't pause; delays apply to replies only", i)
	}

	return mpart.Compile(&mp.spec, engine)
}

// Part is a part of a multipart body. Start one with NewPart.
type Part struct {
	spec mpart.PartSpec
}

// NewPart starts a part.
func NewPart() *Part {
	return &Part{}
}

// Name sets the name of the part, in its Content-Disposition. Every part of a form-data body needs one.
func (p *Part) Name(name string) *Part {
	p.spec.Name = name
	return p
}

// Filename sets the file name of the part, in its Content-Disposition.
func (p *Part) Filename(filename string) *Part {
	p.spec.Filename = filename
	return p
}

// ContentType sets the Content-Type of the part. Without one, a JSON body is application/json, and a part with a
// filename application/octet-stream.
func (p *Part) ContentType(contentType string) *Part {
	p.spec.ContentType = contentType
	return p
}

// Header adds a header to the part.
func (p *Part) Header(key, value string) *Part {
	p.spec.Header = append(p.spec.Header, mpart.Header{Name: key, Value: value})
	return p
}

// Body sets the body of the part, written as it is. Like every body setter, it replaces the body an earlier one set.
func (p *Part) Body(b []byte) *Part {
	p.body(b, mpart.BodyBytes)
	return p
}

// BodyText sets the body of the part as text, a template when the body is templated.
func (p *Part) BodyText(text string) *Part {
	p.body([]byte(text), mpart.BodyText)
	return p
}

// BodyJSON sets the body of the part as the JSON of v. A value that doesn't marshal makes building the mock fail.
func (p *Part) BodyJSON(v any) *Part {
	b, err := json.Marshal(v, json.Deterministic(true))
	p.body(b, mpart.BodyJSON)
	p.spec.Err = err

	return p
}

// BodyFile sets the body of the part to the content of a file, read at each reply. Relative paths start at the
// working directory, or at the mock file's directory for mock files.
func (p *Part) BodyFile(filename string) *Part {
	p.body(nil, mpart.BodyBytes)
	p.spec.File = filename

	return p
}

// Delay makes a reply wait d before writing the part: the parts before it are sent and flushed first, so a client
// reads them while it waits, as in multipart/x-mixed-replace streams.
func (p *Part) Delay(d time.Duration) *Part {
	p.spec.Delay = d
	return p
}

// body sets the body and its kind, and drops what an earlier body setter left.
func (p *Part) body(b []byte, kind mpart.BodyKind) {
	p.spec.Body = b
	p.spec.Kind = kind
	p.spec.File = ""
	p.spec.Err = nil
}
