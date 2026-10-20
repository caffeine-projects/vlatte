// Package mpart writes multipart bodies, for replies and webhooks, with mime/multipart.Writer: their model, the checks
// of the model, and the tables that decode it from mock files. It doesn't parse bodies: net/http parses requests.
package mpart

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/textproto"
	"slices"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/internal/bodyfile"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// Spec is a multipart body to write.
type Spec struct {
	Subtype   string            // such as form-data or mixed: the media type is multipart/<Subtype>
	Boundary  string            // "" draws a random one
	Params    map[string]string // more parameters of the Content-Type, such as type and start for related
	Parts     []*PartSpec
	Templated bool   // the text of the parts is templates: see Compile
	Dir       string // where relative body files open from; "" is the working directory
}

// BodyKind says what the body of a part is.
type BodyKind int

const (
	BodyBytes BodyKind = iota // bytes, written as they are
	BodyText                  // text, a template when the spec is templated
	BodyJSON                  // JSON, written as it is; it defaults the part's content type to application/json
)

// PartSpec is a part of a multipart body.
type PartSpec struct {
	Name        string // the name of a form-data part, in its Content-Disposition
	Filename    string // the filename of its Content-Disposition
	ContentType string
	Header      []Header // more headers, in order
	Body        []byte
	Kind        BodyKind
	File        string        // a file to read the body from, at each Encode
	Delay       time.Duration // how long a streamed reply waits before writing the part
	Err         error         // what went wrong building the part, such as a JSON body that doesn't marshal
}

// Header is a header of a part.
type Header struct {
	Name  string
	Value string
}

// Issue is a problem of a Spec. Path names where it is, as a mock file names its keys: "boundary" or
// "parts[1].name". Message follows the path: "parts[1].name is required in a form-data part". A conflict names the
// other path in With instead.
type Issue struct {
	Code    string // a config problem code, such as invalid_value
	Path    string
	Message string
	With    string
	Hint    string
}

// String formats the issue as its path and message, then its hint.
func (i Issue) String() string {
	s := i.Text("")
	if i.Hint != "" {
		s += "; " + i.Hint
	}

	return s
}

// Text returns the path and message of the issue, with its paths under prefix, such as "response.multipart".
func (i Issue) Text(prefix string) string {
	message := i.Message
	if i.With != "" {
		message = "is set with " + under(prefix, i.With)
	}

	return strings.TrimSpace(under(prefix, i.Path) + " " + message)
}

// under returns path under prefix: "prefix.path", or path when prefix is "".
func under(prefix, path string) string {
	if prefix == "" {
		return path
	}

	return prefix + "." + path
}

// Check returns the issues of s: what makes it a body that can't be written, or not as asked.
func (s *Spec) Check() []Issue {
	var issues []Issue

	add := func(code, path, message, hint string) {
		issues = append(issues, Issue{Code: code, Path: path, Message: message, Hint: hint})
	}

	switch {
	case s.Subtype == "":
		add(config.CodeMissingValue, "type", "is required", "name the subtype, such as form-data or mixed")
	case strings.Contains(s.Subtype, "/"):
		add(config.CodeInvalidValue, "type", fmt.Sprintf("must be a subtype, not %q", s.Subtype), "write the subtype only, such as mixed")
	case mime.FormatMediaType("multipart/"+s.Subtype, nil) == "":
		add(config.CodeInvalidValue, "type", fmt.Sprintf("must be a token, such as mixed, not %q", s.Subtype), "")
	}

	boundary := s.Boundary
	if boundary != "" {
		if err := multipart.NewWriter(io.Discard).SetBoundary(boundary); err != nil {
			add(config.CodeInvalidValue, "boundary",
				fmt.Sprintf("must be 1 to 70 letters, digits or '()+_,-./:=? characters, not ending in a space, not %q", boundary), "")

			boundary = ""
		}
	}

	for _, name := range slices.Sorted(maps.Keys(s.Params)) {
		switch {
		case strings.EqualFold(name, "boundary"):
			add(config.CodeNotAllowed, "params."+name, "can't be set", "set boundary instead")
		case mime.FormatMediaType("multipart/mixed", map[string]string{name: s.Params[name]}) == "":
			add(config.CodeInvalidValue, "params."+name, "is not a parameter of a media type", "")
		}
	}

	if len(s.Parts) == 0 {
		add(config.CodeInvalidValue, "parts", "is empty", "add at least one part")
	}

	for i, p := range s.Parts {
		path := fmt.Sprintf("parts[%d]", i)
		issues = append(issues, p.check(path, s.formData())...)

		// A body that is known now is checked now; a template or a file is checked when it is written.
		static := p.File == "" && (p.Kind != BodyText || !s.Templated)
		if boundary != "" && static && startsALine(p.Body, boundary) {
			add(config.CodeInvalidValue, path+".body", fmt.Sprintf("has a line that starts with the boundary %q, which would end the part there", boundary),
				"choose another boundary, or leave it out for a random one")
		}
	}

	return issues
}

// check returns the issues of a part, at path.
func (p *PartSpec) check(path string, formData bool) []Issue {
	var issues []Issue

	add := func(code, at, message, hint string) {
		issues = append(issues, Issue{Code: code, Path: path + at, Message: message, Hint: hint})
	}

	conflict := func(at, with string) {
		issues = append(issues, Issue{Code: config.CodeConflict, Path: path + at, With: path + with, Hint: "keep one of them"})
	}

	if p.Err != nil {
		add(config.CodeInvalidValue, ".body", p.Err.Error(), "")
	}

	if formData && p.Name == "" {
		add(config.CodeMissingValue, ".name", "is required in a form-data part", "name the form field")
	}

	// Header values are written without the spaces and line breaks around them, so only a line break inside counts.
	// This is the text as written: a template that renders a line break fails when the body is written.
	if strings.ContainsAny(textproto.TrimString(p.ContentType), "\r\n") {
		add(config.CodeInvalidValue, ".content_type", "holds a line break", "")
	}

	if p.Delay < 0 {
		add(config.CodeInvalidValue, ".delay", "must be 0 or more", "")
	}

	for _, h := range p.Header {
		at := ".header." + h.Name

		switch canonical := textproto.CanonicalMIMEHeaderKey(h.Name); {
		case !IsToken(h.Name):
			add(config.CodeInvalidValue, at, "is not a header name", "")
		case strings.ContainsAny(textproto.TrimString(h.Value), "\r\n"):
			add(config.CodeInvalidValue, at, "holds a line break", "")
		case canonical == "Content-Type" && p.ContentType != "":
			conflict(at, ".content_type")
		case canonical == "Content-Disposition" && p.Name != "":
			conflict(at, ".name")
		case canonical == "Content-Disposition" && p.Filename != "":
			conflict(at, ".filename")
		}
	}

	return issues
}

// Doc returns s as a mock file writes it: the object Decode reads. A byte body is written as text, the same bytes.
// The relative body files of its parts are joined to its directory, so they open from the working directory.
func (s *Spec) Doc() map[string]any {
	doc := map[string]any{"type": s.Subtype}
	if s.Boundary != "" {
		doc["boundary"] = s.Boundary
	}

	if len(s.Params) > 0 {
		params := make(map[string]any, len(s.Params))
		for k, v := range s.Params {
			params[k] = v
		}

		doc["params"] = params
	}

	parts := make([]any, len(s.Parts))
	for i, p := range s.Parts {
		parts[i] = p.doc(s.Dir)
	}

	doc["parts"] = parts

	return doc
}

// formData reports whether the body is multipart/form-data. Subtypes ignore case.
func (s *Spec) formData() bool {
	return strings.EqualFold(s.Subtype, "form-data")
}

// doc returns the part as a mock file writes it, its relative body file joined to dir.
func (p *PartSpec) doc(dir string) map[string]any {
	doc := make(map[string]any)

	fields := map[string]string{
		"name": p.Name, "filename": p.Filename, "content_type": p.ContentType, "body_file": bodyfile.Path(dir, p.File),
	}
	for key, v := range fields {
		if v != "" {
			doc[key] = v
		}
	}

	if len(p.Header) > 0 {
		header := make(map[string]any, len(p.Header))
		for _, h := range p.Header {
			switch prev := header[h.Name].(type) {
			case nil:
				header[h.Name] = h.Value
			case string:
				header[h.Name] = []any{prev, h.Value}
			case []any:
				header[h.Name] = append(prev, h.Value)
			}
		}

		doc["header"] = header
	}

	switch {
	case p.File != "":
	case p.Kind == BodyJSON:
		// A file reads a text body as text and rejects a null one, so a JSON text or null is written as its JSON
		// text, typed as JSON: the same bytes and headers.
		v, err := value.DecodeJSON(p.Body)
		switch v.(type) {
		case string, nil:
			doc["body"] = string(p.Body)
			if err == nil && p.ContentType == "" && !p.hasHeader("Content-Type") {
				doc["content_type"] = "application/json"
			}
		default:
			doc["body"] = v
		}
	case len(p.Body) > 0 || p.Kind == BodyText:
		doc["body"] = string(p.Body)
	}

	if p.Delay > 0 {
		doc["delay"] = p.Delay.String()
	}

	return doc
}

// hasHeader reports whether the part sets the header name, in any case.
func (p *PartSpec) hasHeader(name string) bool {
	for _, h := range p.Header {
		if strings.EqualFold(h.Name, name) {
			return true
		}
	}

	return false
}

// startsALine reports whether a line of body starts with "--" and the boundary, as a delimiter line does: a reader
// would end the part there. RFC 2046 keeps such lines out of bodies; "--" and the boundary within a line are fine.
func startsALine(body []byte, boundary string) bool {
	delimiter := []byte("--" + boundary)
	return bytes.HasPrefix(body, delimiter) || bytes.Contains(body, append([]byte("\n"), delimiter...))
}

// IsToken reports whether s is an HTTP token, as header names and methods are.
func IsToken(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range []byte(s) {
		if c <= ' ' || c >= 0x7f || strings.IndexByte(`()<>@,;:\"/[]?={}`, c) >= 0 {
			return false
		}
	}

	return true
}
