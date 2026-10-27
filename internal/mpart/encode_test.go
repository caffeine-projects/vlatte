package mpart

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// _engine compiles the templates of the specs under test.
var _engine = vlatte.MustNewGoTemplateEngine()

func TestEncode_HeaderValuesLoseOuterSpaces(t *testing.T) {
	// genHeaderValue draws no space at either end of a value: a reader trims them, as HTTP header values are.
	e, err := Compile(&Spec{Subtype: "mixed", Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: " a "}}}}}, _engine)
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)

	_, params, err := mime.ParseMediaType(e.ContentType())
	require.NoError(t, err)

	part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextRawPart()
	require.NoError(t, err)
	require.Equal(t, "a", part.Header.Get("X-A"))

	_, err = io.ReadAll(part)
	require.NoError(t, err)
}

func TestEncode_HeaderValuesLoseTheLineBreaksAround(t *testing.T) {
	// A YAML block scalar ends its value with a line break. Header values and content types are written without the
	// spaces and line breaks around them, as net/http writes headers, so only a line break inside fails
	// (TestEncode_Errors).
	tests := []struct {
		name string
		spec *Spec
	}{
		{"as written", &Spec{Subtype: "mixed", Boundary: "b", Parts: []*PartSpec{{ContentType: "text/plain\n", Header: []Header{{Name: "X-A", Value: "v\n"}}}}}},
		{"as rendered", &Spec{Subtype: "mixed", Boundary: "b", Templated: true, Parts: []*PartSpec{{ContentType: "{{ .CT }}\n", Header: []Header{{Name: "X-A", Value: "{{ .V }}\n"}}}}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Compile(tc.spec, _engine)
			require.NoError(t, err)

			body, _, err := e.Encode(map[string]any{"CT": "text/plain", "V": "v"})
			require.NoError(t, err)
			require.Equal(t, "--b\r\nContent-Type: text/plain\r\nX-A: v\r\n\r\n\r\n--b--\r\n", string(body))
		})
	}
}

func TestEncode_FormDataInAnyCase(t *testing.T) {
	// Subtypes ignore case: Form-Data parts are form-data parts, which net/http reads as the form's fields and files.
	// Written as attachments, they would be dropped.
	e, err := Compile(&Spec{Subtype: "Form-Data", Parts: []*PartSpec{
		{Name: "title", Body: []byte("Holiday"), Kind: BodyText},
		{Name: "photo", Filename: "a.png", Body: []byte("PNG")},
	}}, _engine)
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", e.ContentType())
	require.NoError(t, req.ParseMultipartForm(int64(len(body))))
	require.Equal(t, "Holiday", req.PostFormValue("title"))
	require.Equal(t, "a.png", req.MultipartForm.File["photo"][0].Filename)
}

func TestEncode_TheBoundaryWithinALine(t *testing.T) {
	// A delimiter starts a line, so "--" and the boundary within a line don't end a part: the body reads back whole.
	e, err := Compile(&Spec{Subtype: "mixed", Boundary: "b", Parts: []*PartSpec{{Body: []byte("a--b c\r\nd --b--"), Kind: BodyText}}}, _engine)
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)

	part, err := multipart.NewReader(bytes.NewReader(body), "b").NextRawPart()
	require.NoError(t, err)

	got, err := io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, "a--b c\r\nd --b--", string(got))
}

func TestEncode_TemplatedBodyFileName(t *testing.T) {
	// A templated spec renders the name of a body file, not its content: a file may be binary.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("{{ .Name }} as is"), 0o600))

	e, err := Compile(&Spec{Subtype: "mixed", Templated: true, Dir: dir, Parts: []*PartSpec{{File: "{{ .Name }}.txt"}}}, _engine)
	require.NoError(t, err)

	body, _, err := e.Encode(map[string]any{"Name": "a"})
	require.NoError(t, err)
	require.Contains(t, string(body), "\r\n\r\n{{ .Name }} as is\r\n")
}

func TestEncode_TemplatedBodyFileNamesStayInTheDir(t *testing.T) {
	// A body file name rendered from data, such as the request, can't reach out of the spec's directory. A name
	// written in the spec is the author's, so it opens as it is.
	root := t.TempDir()
	dir := filepath.Join(root, "mocks")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"), []byte("in"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "out.txt"), []byte("out"), 0o600))

	encode := func(templated bool, file string, data any) (string, error) {
		e, err := Compile(&Spec{Subtype: "mixed", Templated: templated, Dir: dir, Parts: []*PartSpec{{File: file}}}, _engine)
		require.NoError(t, err)

		body, _, err := e.Encode(data)

		return string(body), err
	}

	body, err := encode(true, "{{ .Name }}", map[string]any{"Name": "in.txt"})
	require.NoError(t, err)
	require.Contains(t, body, "\r\n\r\nin\r\n")

	for _, name := range []string{"../out.txt", filepath.Join(root, "out.txt")} {
		_, err = encode(true, "{{ .Name }}", map[string]any{"Name": name})
		require.ErrorContains(t, err, "multipart: parts[0]: ", name)
	}

	for _, templated := range []bool{false, true} {
		body, err = encode(templated, "../out.txt", nil)
		require.NoError(t, err)
		require.Contains(t, body, "\r\n\r\nout\r\n")
	}
}

func TestEncode_Errors(t *testing.T) {
	// What fails at Encode, rendering with the data of one use, names the part.
	tests := []struct {
		name string
		spec *Spec
		data any
		want string
	}{
		{"a body file that doesn't exist", &Spec{Subtype: "mixed", Parts: []*PartSpec{{Body: []byte("a")}, {File: "no-such-file"}}}, nil, "multipart: parts[1]: open no-such-file: "},
		{"a header that renders a line break", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: "{{ .V }}"}}}}}, map[string]any{"V": "a\nX-B: b"}, "multipart: parts[0]: header.X-A holds a line break"},
		{"a content type that renders a line break", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{ContentType: "{{ .V }}"}}}, map[string]any{"V": "a/b\r\nX: y"}, "multipart: parts[0]: content_type holds a line break"},
		{"a form-data name that renders empty", &Spec{Subtype: "form-data", Templated: true, Parts: []*PartSpec{{Name: "{{ .V }}"}}}, map[string]any{"V": ""}, "multipart: parts[0]: name is empty: a form-data part needs one"},
		{"a template that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Body: []byte("{{ .V.W }}"), Kind: BodyText}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: body:1:5: executing"},
		{"a name that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Name: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: name:1:5: executing"},
		{"a filename that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Filename: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: filename:1:5: executing"},
		{"a content type that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{ContentType: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: content_type:1:5: executing"},
		{"a header that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: "{{ .V.W }}"}}}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: header.X-A:1:5: executing"},
		{"a body file name that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{File: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: body_file:1:5: executing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Compile(tc.spec, _engine)
			require.NoError(t, err)

			_, _, err = e.Encode(tc.data)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCompile_Errors(t *testing.T) {
	// Compile reports every issue of the spec, and template syntax errors, naming where they are.
	_, err := Compile(&Spec{Subtype: "form-data", Boundary: "a ", Parts: []*PartSpec{{Err: errors.New("json: cannot marshal")}}}, _engine)
	require.EqualError(t, err, "multipart: boundary must be 1 to 70 letters, digits or '()+_,-./:=? characters, not ending in a space, not \"a \"; "+
		"parts[0].body json: cannot marshal; parts[0].name is required in a form-data part; name the form field")

	_, err = Compile(&Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Filename: "{{ .A"}}}, _engine)
	require.ErrorContains(t, err, "multipart: parts[0]: filename: template: filename:1: ")

	// Templates compile with the engine of the server: without one, a templated spec can't compile.
	_, err = Compile(&Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Name: "n"}}}, nil)
	require.EqualError(t, err, "multipart: a templated body needs a template engine")
}

func TestEncode_ContentTypeParameters(t *testing.T) {
	// The Content-Type holds the boundary and the parameters, quoted where they need it.
	e, err := Compile(&Spec{
		Subtype: "related", Boundary: "b0",
		Params: map[string]string{"type": "application/xop+xml", "start": "<root>", "start-info": "text/xml"},
		Parts:  []*PartSpec{{Body: []byte("x")}},
	}, _engine)
	require.NoError(t, err)
	require.Equal(t, `multipart/related; boundary=b0; start="<root>"; start-info="text/xml"; type="application/xop+xml"`, e.ContentType())
}

func TestEncode_DispositionOutsideForms(t *testing.T) {
	// Outside form-data, a name or a filename makes an attachment; a part with neither has no disposition.
	e, err := Compile(&Spec{Subtype: "mixed", Boundary: "b", Parts: []*PartSpec{{Name: "n"}, {Filename: `a"b.txt`}, {}}}, _engine)
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)
	require.Equal(t, "--b\r\nContent-Disposition: attachment; name=\"n\"\r\n\r\n"+
		"\r\n--b\r\nContent-Disposition: attachment; filename=\"a\\\"b.txt\"\r\nContent-Type: application/octet-stream\r\n\r\n"+
		"\r\n--b\r\n\r\n"+
		"\r\n--b--\r\n", string(body))
}

// seen is what Go's mime.ParseMediaType reads from a name or filename of a Content-Disposition: the escaper keeps
// quotes and backslashes, and writes CR and LF as %0D and %0A, which nothing decodes.
func seen(s string) string {
	return strings.NewReplacer("\r", "%0D", "\n", "%0A").Replace(s)
}

// contentOf returns the bytes a part's body is: its file's content, or its body.
func contentOf(t require.TestingT, s *Spec, p *PartSpec) []byte {
	if p.File == "" {
		return p.Body
	}

	b, err := os.ReadFile(filepath.Join(s.Dir, p.File))
	require.NoError(t, err)

	return b
}

// lineStarts reports whether a line of b starts with "--" and the boundary. RFC 2046 keeps such lines out of
// bodies, for a reader takes them for delimiters. It is the oracle of the encoder's check, written apart from it.
func lineStarts(b []byte, boundary string) bool {
	return slices.ContainsFunc(bytes.Split(b, []byte("\n")), func(line []byte) bool {
		return bytes.HasPrefix(line, []byte("--"+boundary))
	})
}

// collides reports the first part with a line that starts with the boundary, or -1.
func collides(t require.TestingT, s *Spec, boundary string) int {
	return slices.IndexFunc(s.Parts, func(p *PartSpec) bool { return lineStarts(contentOf(t, s, p), boundary) })
}

// wantContentType is the Content-Type a part is written with: the one set, else a default.
func wantContentType(p *PartSpec) string {
	for _, h := range p.Header {
		if strings.EqualFold(h.Name, "Content-Type") {
			return h.Value
		}
	}

	switch {
	case p.ContentType != "":
		return p.ContentType
	case p.File == "" && p.Kind == BodyJSON:
		return "application/json"
	case p.Filename != "":
		return "application/octet-stream"
	default:
		return ""
	}
}

func TestPropEncode_GoReadsTheSpec(t *testing.T) {
	// MP1: a body reads back, with Go's multipart reader, as the spec says: its parts in order, each with its bytes,
	// headers and Content-Disposition. Form-data bodies read back as their fields and files with
	// http.Request.ParseMultipartForm, which is what servers use. The Content-Type names the subtype, the boundary
	// and the parameters.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")

		e, err := Compile(s, _engine)
		require.NoError(t, err)

		mediaType, params, err := mime.ParseMediaType(e.ContentType())
		require.NoError(t, err)
		require.Equal(t, "multipart/"+strings.ToLower(s.Subtype), mediaType)

		boundary := params["boundary"]
		if s.Boundary != "" {
			require.Equal(t, s.Boundary, boundary)
		}

		for k, v := range s.Params {
			require.Equal(t, v, params[k], "param %s", k)
		}

		body, _, err := e.Encode(nil)
		if i := collides(t, s, boundary); i >= 0 {
			require.ErrorContains(t, err, fmt.Sprintf("multipart: parts[%d]: a line of the body starts with the boundary", i), "MP2")
			return
		}

		require.NoError(t, err)

		r := multipart.NewReader(bytes.NewReader(body), boundary)
		for i, p := range s.Parts {
			part, err := r.NextRawPart()
			require.NoError(t, err, "part %d", i)

			got, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, string(contentOf(t, s, p)), string(got), "part %d body", i)

			kind, disposition, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			switch {
			case strings.EqualFold(s.Subtype, "form-data"):
				require.Equal(t, "form-data", kind)
			case p.Name != "" || p.Filename != "":
				require.Equal(t, "attachment", kind)
			default:
				require.Empty(t, part.Header.Get("Content-Disposition"))
			}

			require.Equal(t, seen(p.Name), disposition["name"], "part %d name", i)
			require.Equal(t, seen(p.Filename), disposition["filename"], "part %d filename", i)
			require.Equal(t, wantContentType(p), part.Header.Get("Content-Type"), "part %d content type", i)

			for _, h := range p.Header {
				if !strings.EqualFold(h.Name, "Content-Type") {
					require.Contains(t, part.Header.Values(h.Name), h.Value, "part %d header %s", i, h.Name)
				}
			}
		}

		_, err = r.NextRawPart()
		require.ErrorIs(t, err, io.EOF)

		if strings.EqualFold(s.Subtype, "form-data") {
			requireForm(t, s, e.ContentType(), body)
		}
	})
}

// requireForm checks that net/http reads a form-data body as the spec's fields and files.
func requireForm(t require.TestingT, s *Spec, contentType string, body []byte) {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	require.NoError(t, req.ParseMultipartForm(int64(len(body))))

	values := make(map[string][]string)
	files := make(map[string][]string)

	for _, p := range s.Parts {
		name := seen(p.Name)
		if p.Filename == "" {
			values[name] = append(values[name], string(contentOf(t, s, p)))
			continue
		}

		files[name] = append(files[name], filepath.Base(seen(p.Filename))+"="+string(contentOf(t, s, p)))
	}

	require.Equal(t, values, map[string][]string(req.MultipartForm.Value))

	got := make(map[string][]string)
	for name, fhs := range req.MultipartForm.File {
		for _, fh := range fhs {
			f, err := fh.Open()
			require.NoError(t, err)

			b, err := io.ReadAll(f)
			require.NoError(t, err)
			require.NoError(t, f.Close())

			got[name] = append(got[name], fh.Filename+"="+string(b))
		}
	}

	require.Equal(t, files, got)
}

func TestPropEncode_ALineStartingWithTheBoundaryFails(t *testing.T) {
	// MP2: a line of a body can't start with the boundary, for a reader would end the part there; "--" and the
	// boundary within a line are fine. A body known when the spec is checked fails the check, so a mock with it
	// doesn't build; a body file fails when it is written. Either way, the error names the first such part.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		s.Boundary = genBoundary().Draw(t, "boundary")

		i := rapid.IntRange(0, len(s.Parts)-1).Draw(t, "part")
		content := contentOf(t, s, s.Parts[i])
		at := rapid.IntRange(0, len(content)).Draw(t, "at")
		before := rapid.SampledFrom([]string{"", "x", "\n", "\r\n"}).Draw(t, "before")
		content = slices.Concat(content[:at], []byte(before+"--"+s.Boundary), content[at:])

		p := s.Parts[i]
		if rapid.Bool().Draw(t, "in a file") {
			p.File = fmt.Sprintf("mp2-%d.bin", i)
			require.NoError(t, os.WriteFile(filepath.Join(dir, p.File), content, 0o600))
		} else {
			p.File, p.Body, p.Kind = "", content, BodyBytes
		}

		static := slices.IndexFunc(s.Parts, func(p *PartSpec) bool { return p.File == "" && lineStarts(p.Body, s.Boundary) })

		e, err := Compile(s, _engine)
		if static >= 0 {
			require.ErrorContains(t, err, fmt.Sprintf("multipart: parts[%d].body has a line that starts with the boundary", static))
			return
		}

		require.NoError(t, err)

		_, _, err = e.Encode(nil)
		if first := collides(t, s, s.Boundary); first >= 0 {
			require.ErrorContains(t, err, fmt.Sprintf("multipart: parts[%d]: a line of the body starts with the boundary", first))
			return
		}

		require.NoError(t, err)
	})
}

func TestPropEncode_PausesFollowTheirDelimiterLine(t *testing.T) {
	// MP3: a delayed part pauses right after the delimiter line that opens it, so the part before it is whole on
	// the client during the wait. Parts without a delay don't pause.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")

		e, err := Compile(s, _engine)
		require.NoError(t, err)

		_, params, err := mime.ParseMediaType(e.ContentType())
		require.NoError(t, err)

		boundary := params["boundary"]
		if collides(t, s, boundary) >= 0 {
			return // MP2
		}

		body, pauses, err := e.Encode(nil)
		require.NoError(t, err)

		var want []Pause

		offset := 0
		for i, p := range s.Parts {
			delimiter := "\r\n--" + boundary + "\r\n"
			if i == 0 {
				delimiter = "--" + boundary + "\r\n"
			}

			at := bytes.Index(body[offset:], []byte(delimiter))
			require.GreaterOrEqual(t, at, 0, "the delimiter of part %d", i)

			offset += at + len(delimiter)
			if p.Delay > 0 {
				want = append(want, Pause{At: offset, Delay: p.Delay})
			}
		}

		require.Equal(t, want, pauses)
	})
}

func TestPropEncode_TemplatesAreTheText(t *testing.T) {
	// MP4: in a templated spec, the text of the parts renders with the data: names, filenames, header values and
	// text bodies. Byte and JSON bodies and the content of files are written as they are, for they may be binary,
	// such as the JPEG frames of a camera stream. Text that isn't templated is written as it is, {{ included.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		s.Templated = rapid.Bool().Draw(t, "templated")
		x := genHeaderValue().Draw(t, "x")

		const action = "{{ .X }}"

		maybe := func(label, text string) string {
			if rapid.Bool().Draw(t, label) {
				return text + action
			}

			return text
		}

		for _, p := range s.Parts {
			p.Name = maybe("templated name", p.Name)
			p.Filename = maybe("templated filename", p.Filename)

			for i := range p.Header {
				p.Header[i].Value = maybe("templated header", p.Header[i].Value)
			}

			switch {
			case p.Kind == BodyText && p.File == "":
				// Generated text may hold {{ of its own, which isn't a template.
				p.Body = []byte(maybe("templated body", genHeaderValue().Draw(t, "text")))
			case p.Kind == BodyJSON && p.File == "":
				p.Body, _ = json.Marshal(action)
			}
		}

		render := func(text string) string {
			if s.Templated {
				return strings.ReplaceAll(text, action, x)
			}

			return text
		}

		written := func(p *PartSpec) []byte {
			if p.Kind == BodyText && p.File == "" {
				return []byte(render(string(p.Body)))
			}

			return contentOf(t, s, p)
		}

		if s.Boundary != "" && slices.ContainsFunc(s.Parts, func(p *PartSpec) bool { return lineStarts(written(p), s.Boundary) }) {
			return // MP2
		}

		e, err := Compile(s, _engine)
		require.NoError(t, err)

		_, params, err := mime.ParseMediaType(e.ContentType())
		require.NoError(t, err)

		if slices.ContainsFunc(s.Parts, func(p *PartSpec) bool { return lineStarts(written(p), params["boundary"]) }) {
			return // MP2
		}

		body, _, err := e.Encode(map[string]any{"X": x})
		require.NoError(t, err)

		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for i, p := range s.Parts {
			part, err := r.NextRawPart()
			require.NoError(t, err)

			got, err := io.ReadAll(part)
			require.NoError(t, err)

			want := contentOf(t, s, p)
			if p.Kind == BodyText && p.File == "" {
				want = []byte(render(string(p.Body)))
			}

			require.Equal(t, string(want), string(got), "part %d body", i)

			_, disposition, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			require.Equal(t, seen(render(p.Name)), disposition["name"], "part %d name", i)
			require.Equal(t, seen(render(p.Filename)), disposition["filename"], "part %d filename", i)

			for _, h := range p.Header {
				require.Contains(t, part.Header.Values(h.Name), render(h.Value), "part %d header %s", i, h.Name)
			}
		}
	})
}

func BenchmarkEncode(b *testing.B) {
	// A 10-part reply, written at each response.
	s := &Spec{Subtype: "mixed"}
	for range 10 {
		s.Parts = append(s.Parts, &PartSpec{Name: "p", ContentType: "application/octet-stream", Body: bytes.Repeat([]byte("x"), 4<<10)})
	}

	e, err := Compile(s, _engine)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, _, err := e.Encode(nil); err != nil {
			b.Fatal(err)
		}
	}
}

// Generators

// _genSubtypes are the subtypes specs draw, besides random tokens. Subtypes ignore case.
var _genSubtypes = []string{"form-data", "Form-Data", "FORM-DATA", "mixed", "related", "alternative", "x-mixed-replace"}

// genBoundary draws a boundary that Writer.SetBoundary takes: 1 to 70 of its characters, not ending in a space.
func genBoundary() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9A-Za-z'()+_,\-./:=?]([0-9A-Za-z'()+_,\-./:=? ]{0,68}[0-9A-Za-z'()+_,\-./:=?])?`)
}

// genText draws text for names and filenames: unicode, and the characters a Content-Disposition quotes or escapes,
// CR and LF included.
func genText() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-"\\;=/éü\r\n]{1,10}`)
}

// genHeaderValue draws a header value: no CR or LF, which Check rejects, and no space at either end, which readers
// trim (TestEncode_HeaderValuesLoseOuterSpaces).
func genHeaderValue() *rapid.Generator[string] {
	return rapid.StringMatching(`([A-Za-z0-9._\-"\\;=/:<>@éü]([A-Za-z0-9 ._\-"\\;=/:<>@éü]{0,8}[A-Za-z0-9._\-"\\;=/:<>@éü])?)?`)
}

// genBytes draws a body with line breaks and dashes, as delimiter lines have.
func genBytes() *rapid.Generator[[]byte] {
	return rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"\r\n", "--", "\r\n--", "\x00", "\xff", "a", "{{ .X }}"}), 0, 12),
		func(chunks []string) []byte {
			var b []byte
			for _, c := range chunks {
				b = append(b, c...)
			}

			return b
		})
}

// genJSON draws a value of the data model, for JSON bodies.
func genJSON() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Map(rapid.String(), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Just[any](nil),
		rapid.Map(rapid.SliceOfN(rapid.Int64(), 0, 3), func(l []int64) any { return toAny(l) }),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-z]{1,4}`), rapid.String(), 0, 3), func(m map[string]string) any {
			out := make(map[string]any, len(m))
			for k, v := range m {
				out[k] = v
			}

			return out
		}),
	)
}

func toAny[E any](s []E) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}

	return out
}

// genPart draws a part that checks clean. Body files go to dir.
func genPart(formData bool, dir string, i int) *rapid.Generator[*PartSpec] {
	return rapid.Custom(func(t *rapid.T) *PartSpec {
		p := new(PartSpec)
		if formData || rapid.Bool().Draw(t, "has name") {
			p.Name = genText().Draw(t, "name")
		}

		if rapid.Bool().Draw(t, "has filename") {
			p.Filename = genText().Draw(t, "filename")
		}

		if rapid.Bool().Draw(t, "has content type") {
			p.ContentType = rapid.SampledFrom([]string{"text/plain", "image/png; x=1", "application/vnd.a+json"}).Draw(t, "content type")
		}

		for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"X-A", "x-b", "Content-Id"}), 0, 2,
			func(s string) string { return s }).Draw(t, "header names") {
			for range rapid.IntRange(1, 2).Draw(t, "header values") {
				p.Header = append(p.Header, Header{Name: name, Value: genHeaderValue().Draw(t, "header value")})
			}
		}

		// A Content-Type header stands in for content_type only when there is none: Check rejects both.
		if p.ContentType == "" && rapid.Bool().Draw(t, "content type header") {
			p.Header = append(p.Header, Header{Name: "content-type", Value: "text/csv"})
		}

		switch rapid.IntRange(0, 3).Draw(t, "body kind") {
		case 0:
			p.Body, p.Kind = genBytes().Draw(t, "bytes"), BodyBytes
		case 1:
			p.Body, p.Kind = []byte(rapid.String().Draw(t, "text")), BodyText
		case 2:
			b, err := json.Marshal(genJSON().Draw(t, "json"), json.Deterministic(true))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			p.Body, p.Kind = b, BodyJSON
		default:
			p.File = fmt.Sprintf("part%d.bin", i)
			if err := os.WriteFile(filepath.Join(dir, p.File), genBytes().Draw(t, "file content"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}

		if rapid.Bool().Draw(t, "has delay") {
			p.Delay = time.Duration(rapid.Int64Range(1, int64(time.Hour)).Draw(t, "delay"))
		}

		return p
	})
}

// genSpec draws a spec that checks clean, with its body files in dir.
func genSpec(dir string) *rapid.Generator[*Spec] {
	return rapid.Custom(func(t *rapid.T) *Spec {
		s := &Spec{Dir: dir}

		s.Subtype = rapid.OneOf(rapid.SampledFrom(_genSubtypes), rapid.StringMatching(`[a-z][a-z0-9.+-]{0,8}`)).Draw(t, "subtype")
		if rapid.Bool().Draw(t, "has boundary") {
			s.Boundary = genBoundary().Draw(t, "boundary")
		}

		if rapid.Bool().Draw(t, "has params") {
			s.Params = map[string]string{
				"type":  rapid.SampledFrom([]string{"application/json", "text/xml"}).Draw(t, "type"),
				"start": "<" + rapid.StringMatching(`[a-z]{1,6}`).Draw(t, "start") + ">",
			}
		}

		for i := range rapid.IntRange(1, 8).Draw(t, "parts") {
			s.Parts = append(s.Parts, genPart(strings.EqualFold(s.Subtype, "form-data"), dir, i).Draw(t, "part"))
		}

		// A body with a line that starts with the boundary doesn't check clean: MP2 covers it.
		if slices.ContainsFunc(s.Parts, func(p *PartSpec) bool { return p.File == "" && lineStarts(p.Body, s.Boundary) }) {
			s.Boundary = ""
		}

		return s
	})
}
