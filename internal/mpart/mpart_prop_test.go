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
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

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

func TestPropDecode_ReadsWhatDocWrites(t *testing.T) {
	// MP5: Decode reads a spec's Doc back as the same spec: the same Doc, and the same bytes on the wire. A byte
	// body comes back as text, which writes the same bytes.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		s.Boundary = genBoundary().Draw(t, "boundary")

		for _, p := range s.Parts {
			// A mock file is text, so JSON and YAML can't hold such a body: it goes through Go only.
			if !utf8.Valid(p.Body) {
				p.Body = []byte("text")
			}
		}

		// A mock file is JSON or YAML, so the document goes through JSON as a file does.
		data, err := json.Marshal(s.Doc(), json.Deterministic(true))
		require.NoError(t, err)

		read, err := value.DecodeJSON(data)
		require.NoError(t, err)

		got, err := Decode(config.Value{Key: "multipart", Origin: "doc", Any: read})
		if slices.ContainsFunc(s.Parts, func(p *PartSpec) bool { return p.File == "" && lineStarts(p.Body, s.Boundary) }) {
			require.ErrorContains(t, err, "has a line that starts with the boundary", "MP2")
			return
		}

		require.NoError(t, err)

		got.Dir = s.Dir
		require.Equal(t, s.Doc(), got.Doc())

		want, err := Compile(s, _engine)
		require.NoError(t, err)

		decoded, err := Compile(got, _engine)
		require.NoError(t, err)

		wantBody, wantPauses, wantErr := want.Encode(nil)
		gotBody, gotPauses, gotErr := decoded.Encode(nil)

		require.Equal(t, wantErr, gotErr)
		require.Equal(t, string(wantBody), string(gotBody))
		require.Equal(t, wantPauses, gotPauses)
		require.Equal(t, want.ContentType(), decoded.ContentType())
	})
}

// specFault is an invalid setting injected into a valid multipart document, and the problem it must cause.
type specFault struct {
	area   string // what it touches, so faults drawn together never touch the same thing
	code   string
	key    string
	inject func(doc map[string]any)
}

// _specFaults touch distinct settings of a valid multipart document. Each document has one part at least.
var _specFaults = []specFault{
	{"type", config.CodeMissingValue, "m.type", func(doc map[string]any) { delete(doc, "type") }},
	{"type", config.CodeInvalidValue, "m.type", func(doc map[string]any) { doc["type"] = "multipart/mixed" }},
	{"boundary", config.CodeInvalidValue, "m.boundary", func(doc map[string]any) { doc["boundary"] = "ends in a space " }},
	{"params", config.CodeNotAllowed, "m.params.boundary", func(doc map[string]any) { doc["params"] = map[string]any{"boundary": "x"} }},
	{"parts", config.CodeInvalidValue, "m.parts", func(doc map[string]any) { doc["parts"] = []any{} }},
	{"parts", config.CodeConflict, "m.parts[0].header.Content-Type", func(doc map[string]any) {
		p := doc["parts"].([]any)[0].(map[string]any)
		p["content_type"] = "a/b"
		p["header"] = map[string]any{"Content-Type": "c/d"}
	}},
	{"parts", config.CodeInvalidValue, "m.parts[0].header.X-N", func(doc map[string]any) {
		doc["parts"].([]any)[0].(map[string]any)["header"] = map[string]any{"X-N": "a\nb"}
	}},
	{"parts", config.CodeInvalidValue, "m.parts[0].delay", func(doc map[string]any) { doc["parts"].([]any)[0].(map[string]any)["delay"] = int64(-5) }},
	{"parts", config.CodeConflict, "m.parts[0].body_file", func(doc map[string]any) {
		p := doc["parts"].([]any)[0].(map[string]any)
		p["body"] = "x"
		p["body_file"] = "x.bin"
	}},
	{"parts", config.CodeUnknownKey, "m.parts[0].nmae", func(doc map[string]any) { doc["parts"].([]any)[0].(map[string]any)["nmae"] = "x" }},
}

func TestPropDecode_EveryFaultOnce(t *testing.T) {
	// MP5: each fault of a multipart document is reported once, with its code and key, and nothing else is: the
	// checks of the model don't repeat what decoding already found.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		for _, p := range s.Parts {
			p.Body = []byte("text")
		}

		doc := s.Doc()

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_specFaults), 1, len(_specFaults),
			func(f specFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(doc)
			want = append(want, f.code+" "+f.key)
		}

		_, err := Decode(config.Value{Key: "m", Origin: "doc", Any: doc})

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)

		got := make([]string, 0, len(cfgErr.Problems))
		for _, p := range cfgErr.Problems {
			require.Equal(t, "doc", p.Origin)
			got = append(got, p.Code+" "+p.Key)
		}

		require.ElementsMatch(t, want, got)
	})
}

func FuzzMultipartDecode(f *testing.F) {
	// MP6: no document panics Decode, and every problem is keyed under the object's key.
	for _, seed := range []string{
		`{"type": "mixed", "parts": [{"body": "x"}]}`,
		`{"type": "form-data", "boundary": "b", "params": {"start": "<a>"}, "parts": [{"name": "a", "filename": "a.png", "body_file": "a.png", "delay": "1s"}]}`,
		`{"type": "related", "parts": [{"header": {"content-id": ["<a>", "<b>"]}, "body": {"id": 1}}]}`,
		`{"type": 1, "parts": {}}`,
		`{"parts": [null, 1, "x", {"body": null}]}`,
		`[]`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := value.DecodeJSON(data)
		if err != nil {
			return
		}

		_, err = Decode(config.Value{Key: "m", Origin: "fuzz", Any: doc})
		if err == nil {
			return
		}

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)

		for _, p := range cfgErr.Problems {
			require.True(t, p.Key == "m" || strings.HasPrefix(p.Key, "m.") || strings.HasPrefix(p.Key, "m["), "key %q", p.Key)
			require.NotEmpty(t, p.Code)
		}
	})
}
