package mpart

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// _quoteEscaper escapes the name and filename of a Content-Disposition as multipart.FileContentDisposition does.
var _quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

// Pause is where a streamed body waits: At is the offset of the bytes written after the wait. A part's pause comes
// right after the delimiter line that opens it, so the part before is whole on the client while it waits.
type Pause struct {
	At    int
	Delay time.Duration
}

// Encoder writes the body of a Spec. It is safe for concurrent use.
type Encoder struct {
	subtype     string
	boundary    string
	contentType string
	dir         string
	parts       []compiledPart
}

// compiledPart is a part with its templates parsed.
type compiledPart struct {
	name        text
	filename    text
	contentType text
	header      []compiledHeader
	kind        BodyKind
	body        text // the body as text: a template when the spec is templated and the body is text
	file        text // the name of the body file, when there is one
	hasFile     bool
	delay       time.Duration
}

type compiledHeader struct {
	name  string
	value text
}

// text is a text of a part: a template, or the text itself.
type text struct {
	raw  string
	tmpl *template.Template
}

// Compile checks s, parses its templates and draws a boundary when s has none. The Encoder copies s: changing s
// afterwards doesn't change it.
//
// When s is templated, its text is Go templates: names, filenames, content types, header values, text bodies, and
// body file names. Byte and JSON bodies, and the content of body files, are written as they are.
func Compile(s *Spec) (*Encoder, error) {
	if issues := s.Check(); len(issues) > 0 {
		texts := make([]string, len(issues))
		for i, issue := range issues {
			texts[i] = issue.String()
		}

		return nil, errors.New("multipart: " + strings.Join(texts, "; "))
	}

	e := &Encoder{subtype: s.Subtype, boundary: s.Boundary, dir: s.Dir, parts: make([]compiledPart, len(s.Parts))}
	if e.boundary == "" {
		e.boundary = multipart.NewWriter(io.Discard).Boundary()
	}

	params := maps.Clone(s.Params)
	if params == nil {
		params = make(map[string]string, 1)
	}

	params["boundary"] = e.boundary
	e.contentType = mime.FormatMediaType("multipart/"+s.Subtype, params)

	for i, p := range s.Parts {
		part, err := compilePart(p, s.Templated)
		if err != nil {
			return nil, fmt.Errorf("multipart: parts[%d]: %w", i, err)
		}

		e.parts[i] = part
	}

	return e, nil
}

func compilePart(p *PartSpec, templated bool) (compiledPart, error) {
	var err error

	parse := func(field, s string) text {
		t := text{raw: s}
		if templated && err == nil {
			if t.tmpl, err = template.New(field).Parse(s); err != nil {
				err = fmt.Errorf("%s: %w", field, err)
			}
		}

		return t
	}

	part := compiledPart{
		name:        parse("name", p.Name),
		filename:    parse("filename", p.Filename),
		contentType: parse("content_type", p.ContentType),
		kind:        p.Kind,
		hasFile:     p.File != "",
		delay:       p.Delay,
	}

	for _, h := range p.Header {
		part.header = append(part.header, compiledHeader{name: h.Name, value: parse("header."+h.Name, h.Value)})
	}

	switch {
	case part.hasFile:
		part.file = parse("body_file", p.File)
	case p.Kind == BodyText:
		part.body = parse("body", string(p.Body))
	default:
		part.body = text{raw: string(p.Body)}
	}

	return part, err
}

// ContentType returns the Content-Type of the body: multipart/<subtype>, with its boundary and parameters.
func (e *Encoder) ContentType() string {
	return e.contentType
}

// Encode writes the body, with data for its templates. It returns the body, and where a streamed body waits.
func (e *Encoder) Encode(data any) ([]byte, []Pause, error) {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	if err := w.SetBoundary(e.boundary); err != nil {
		return nil, nil, fmt.Errorf("multipart: %w", err)
	}

	var pauses []Pause

	for i := range e.parts {
		p := &e.parts[i]

		header, err := p.headers(data, e.subtype == "form-data")
		if err != nil {
			return nil, nil, fmt.Errorf("multipart: parts[%d]: %w", i, err)
		}

		body, err := p.content(data, e.dir)
		if err != nil {
			return nil, nil, fmt.Errorf("multipart: parts[%d]: %w", i, err)
		}

		if bytes.Contains(body, []byte("--"+e.boundary)) {
			return nil, nil, fmt.Errorf("multipart: parts[%d]: the body holds the boundary %q, so the part would end there", i, e.boundary)
		}

		// CreatePart writes the delimiter line, "--boundary" after a line break unless it is the first part.
		at := buf.Len() + len("--"+e.boundary+"\r\n")
		if i > 0 {
			at += len("\r\n")
		}

		pw, err := w.CreatePart(header)
		if err != nil {
			return nil, nil, fmt.Errorf("multipart: parts[%d]: %w", i, err)
		}

		if p.delay > 0 {
			pauses = append(pauses, Pause{At: at, Delay: p.delay})
		}

		if _, err := pw.Write(body); err != nil {
			return nil, nil, fmt.Errorf("multipart: parts[%d]: %w", i, err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, nil, fmt.Errorf("multipart: %w", err)
	}

	return buf.Bytes(), pauses, nil
}

// headers renders the headers of the part.
func (p *compiledPart) headers(data any, formData bool) (textproto.MIMEHeader, error) {
	name, err := p.name.render(data)
	if err != nil {
		return nil, err
	}

	filename, err := p.filename.render(data)
	if err != nil {
		return nil, err
	}

	contentType, err := p.contentType.render(data)
	if err != nil {
		return nil, err
	}

	h := make(textproto.MIMEHeader)

	for _, header := range p.header {
		v, err := header.value.render(data)
		if err != nil {
			return nil, err
		}

		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("header.%s holds a line break", header.name)
		}

		h.Add(header.name, v)
	}

	switch {
	case formData && name == "":
		return nil, errors.New("name is empty: a form-data part needs one")
	case formData:
		h.Set("Content-Disposition", disposition("form-data", name, filename))
	case name != "" || filename != "":
		h.Set("Content-Disposition", disposition("attachment", name, filename))
	}

	switch {
	case strings.ContainsAny(contentType, "\r\n"):
		return nil, errors.New("content_type holds a line break")
	case contentType != "":
		h.Set("Content-Type", contentType)
	case h.Get("Content-Type") != "":
	case p.kind == BodyJSON:
		h.Set("Content-Type", "application/json")
	case filename != "":
		h.Set("Content-Type", "application/octet-stream")
	}

	return h, nil
}

// content returns the body of the part: its text, rendered, its bytes, or the content of its file.
func (p *compiledPart) content(data any, dir string) ([]byte, error) {
	if p.hasFile {
		name, err := p.file.render(data)
		if err != nil {
			return nil, err
		}

		if dir != "" && !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}

		return os.ReadFile(name)
	}

	s, err := p.body.render(data)

	return []byte(s), err
}

// render returns the text, or the template's output with data.
func (t text) render(data any) (string, error) {
	if t.tmpl == nil {
		return t.raw, nil
	}

	var b strings.Builder
	if err := t.tmpl.Execute(&b, data); err != nil {
		return "", err
	}

	return b.String(), nil
}

// disposition returns a Content-Disposition with a name and a filename, either of which may be "".
func disposition(kind, name, filename string) string {
	var b strings.Builder

	b.WriteString(kind)

	if name != "" {
		b.WriteString(`; name="` + _quoteEscaper.Replace(name) + `"`)
	}

	if filename != "" {
		b.WriteString(`; filename="` + _quoteEscaper.Replace(filename) + `"`)
	}

	return b.String()
}
