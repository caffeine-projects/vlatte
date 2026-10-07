package mockhttp

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestPropForms_TemplatesReadTheForm(t *testing.T) {
	// M24: a reply template reads the form as net/http parsed it: .Request.PostForm holds the text fields, and
	// .Request.FormFile the first file of a field, as http.Request.FormFile picks it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		form := genForm().Draw(t, "form")
		contentType, body := form.encode(t)

		fields := make([]string, 0, len(form.fields)+1)
		files := make([]string, 0, len(form.files)+1)

		for _, f := range form.fields {
			fields = append(fields, f.name)
		}

		for _, f := range form.files {
			files = append(files, f.field)
		}

		// A name that isn't in the form reads as nothing.
		fields = append(fields, "absent")
		files = append(files, "absent")

		var tmpl strings.Builder
		for i, name := range fields {
			fmt.Fprintf(&tmpl, "field%d={{ .Request.PostForm.Get %q }}\n", i, name)
		}

		for i, name := range files {
			fmt.Fprintf(&tmpl, `file%d={{ with .Request.FormFile %q }}{{ .Filename }}|{{ .Size }}|{{ .Header.Get "Content-Type" }}{{ end }}`+"\n", i, name)
		}

		r, err := stdlibForm(contentType, body)
		require.NoError(t, err)

		var want strings.Builder
		for i, name := range fields {
			fmt.Fprintf(&want, "field%d=%s\n", i, r.PostForm.Get(name))
		}

		for i, name := range files {
			fmt.Fprintf(&want, "file%d=", i)

			if fh := r.MultipartForm.File[name]; len(fh) > 0 {
				fmt.Fprintf(&want, "%s|%d|%s", fh[0].Filename, fh[0].Size, fh[0].Header.Get(HeaderContentType))
			}

			want.WriteString("\n")
		}

		scope, err := m.Mock(Postf("/form").Reply(OK().BodyTemplate(tmpl.String())))
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		res, got := post(t, m.URL("/form"), contentType, body)
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", got)
		require.Equal(t, want.String(), string(got))
	})
}

func TestPropForms_BrokenBodiesNeverMatchAPartialForm(t *testing.T) {
	// M25: a form body that is cut short or corrupted is matched on what net/http parses, all or nothing: a field
	// read before the damage never matches once the body fails to parse. The search goes on to the catch-all, a
	// warning says why, and nothing panics.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		form := genForm().Draw(t, "form")
		field := formField{name: genFormName().Draw(t, "matched name"), value: rapid.StringN(1, 8, -1).Draw(t, "matched value")}
		at := rapid.IntRange(0, len(form.fields)).Draw(t, "matched field position")
		form.fields = slices.Insert(form.fields, at, field)

		contentType, body := form.encode(t)

		// The close delimiter ends the body; a cut before its end loses it.
		end := bytes.LastIndex(body, []byte("--")) + 2
		if rapid.Bool().Draw(t, "cut") {
			body = body[:rapid.IntRange(1, end-1).Draw(t, "cut at")]
		} else {
			body = slices.Clone(body)
			for range rapid.IntRange(1, 3).Draw(t, "corruptions") {
				body[rapid.IntRange(0, len(body)-1).Draw(t, "corrupt at")] = rapid.Byte().Draw(t, "corrupt with")
			}
		}

		scope, err := m.Mock(
			Postf("/form").FormField(field.name, match.StrictEqual(field.value)).Reply(OK().BodyText("form")),
			Postf("/form").Priority(1).Reply(Accepted().BodyText("catch-all")),
		)
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		// The oracle is net/http's parse, except that a body without its close delimiter keeps no field:
		// ReadForm keeps the parts before some cuts (TestForms_ACutFormKeepsNoField).
		r, parseErr := stdlibForm(contentType, body)
		if !bytes.Contains(body, []byte("--"+boundaryOf(t, contentType)+"--")) {
			parseErr = errFormCut
		}

		want := "catch-all"
		if parseErr == nil && r.PostForm.Get(field.name) == field.value {
			want = "form"
		}

		warnings := strings.Count(logs.String(), "request_body_parser")

		_, got := post(t, m.URL("/form"), contentType, body)
		require.Equal(t, want, string(got))

		if parseErr != nil {
			require.Greater(t, strings.Count(logs.String(), "request_body_parser"), warnings, "a warning says why")
		}

		require.NotContains(t, logs.String(), "panic during request matching")
	})
}

// boundaryOf returns the boundary of a multipart content type.
func boundaryOf(t require.TestingT, contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)

	return params["boundary"]
}

// filePatternModel is a FilePattern as plain values, with the oracle that says which files it matches.
type filePatternModel struct {
	filename    *string
	contentType *string
	header      *string // X-A
	body        *string
	every       bool
}

func genFilePatternModel() *rapid.Generator[filePatternModel] {
	return rapid.Custom(func(t *rapid.T) filePatternModel {
		maybe := func(label string, values []string) *string {
			if !rapid.Bool().Draw(t, label+" set") {
				return nil
			}

			return new(rapid.SampledFrom(values).Draw(t, label))
		}

		return filePatternModel{
			filename:    maybe("filename", []string{"a.png", "b.txt", "c"}),
			contentType: maybe("content type", []string{"", "image/png", "text/plain"}),
			header:      maybe("header", []string{"", "1", "2"}),
			body:        maybe("body", []string{"", "hello", "\x00\xff", `{"id":1}`}),
			every:       rapid.Bool().Draw(t, "every"),
		}
	})
}

// genPatternFile draws a file of field f, from the same values the patterns compare with.
func genPatternFile() *rapid.Generator[formFile] {
	return rapid.Custom(func(t *rapid.T) formFile {
		file := formFile{
			field:       "f",
			filename:    rapid.SampledFrom([]string{"a.png", "b.txt", "c"}).Draw(t, "filename"),
			contentType: rapid.SampledFrom([]string{"", "image/png", "IMAGE/PNG; x=1", "text/plain"}).Draw(t, "content type"),
			body:        []byte(rapid.SampledFrom([]string{"", "hello", "\x00\xff", `{"id":1}`}).Draw(t, "body")),
		}

		if v := rapid.SampledFrom([]string{"", "1", "2"}).Draw(t, "header"); v != "" {
			file.header = map[string]string{"X-A": v}
		}

		return file
	})
}

// matches is the oracle: whether one file has every condition, written without the code under test.
func (p filePatternModel) matches(file formFile) bool {
	mediaType, _, _ := strings.Cut(file.contentType, ";")

	return (p.filename == nil || *p.filename == file.filename) &&
		(p.contentType == nil || *p.contentType == strings.ToLower(strings.TrimSpace(mediaType))) &&
		(p.header == nil || *p.header == file.header["X-A"]) &&
		(p.body == nil || *p.body == string(file.body))
}

// spec is the pattern in a mock file. A literal is compared as is, as StrictEqual does with text.
func (p filePatternModel) spec() map[string]any {
	spec := make(map[string]any)
	if p.filename != nil {
		spec["filename"] = *p.filename
	}

	if p.contentType != nil {
		spec["content_type"] = *p.contentType
	}

	if p.header != nil {
		spec["header"] = map[string]any{"x-a": *p.header}
	}

	if p.body != nil {
		spec["body"] = *p.body
	}

	if p.every {
		spec["every"] = true
	}

	return spec
}

func (p filePatternModel) pattern() *FilePattern {
	return p.patternWith(func(v any) vlatte.Matcher { return match.StrictEqual(v) })
}

// patternWith builds the pattern with a matcher of the literals, such as match.Equal, which mock files use.
func (p filePatternModel) patternWith(literal func(v any) vlatte.Matcher) *FilePattern {
	f := FileWith()
	if p.filename != nil {
		f.Filename(literal(*p.filename))
	}

	if p.contentType != nil {
		f.ContentType(literal(*p.contentType))
	}

	if p.header != nil {
		f.Header("x-a", literal(*p.header))
	}

	if p.body != nil {
		f.Body(literal(*p.body))
	}

	if p.every {
		f.Every()
	}

	return f
}

func TestPropFormFile_SomeOrEveryFileMatches(t *testing.T) {
	// M19: FormFile matches iff some file of the field has every condition of the pattern, or, with Every, the field
	// has a file and all of its files do. Files of other fields never count. A pattern means the same in Go and in a
	// mock file.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		model := genFilePatternModel().Draw(t, "pattern")
		fromFile := rapid.Bool().Draw(t, "from a mock file")

		// A mock file is text: JSON and YAML can't hold the invalid UTF-8 of this body, which Go patterns cover.
		if fromFile && model.body != nil && !utf8.ValidString(*model.body) {
			model.body = new("hello")
		}
		files := rapid.SliceOfN(genPatternFile(), 0, 3).Draw(t, "files")
		others := rapid.SliceOfN(genPatternFile(), 0, 2).Draw(t, "files of another field")

		some, every := false, len(files) > 0
		for _, f := range files {
			some = some || model.matches(f)
			every = every && model.matches(f)
		}

		want := some
		if model.every {
			want = every
		}

		for i := range others {
			others[i].field = "g"
		}

		var builder vlatte.Builder[*Mock, *Server] = Postf("/files").FormFile("f", model.pattern()).Reply(OK())
		if fromFile {
			doc := map[string]any{
				"request":  map[string]any{"method": "POST", "path": "/files", "form_file": map[string]any{"f": model.spec()}},
				"response": map[string]any{"status": int64(200)},
			}
			format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
			builder = docBuilder{mockDoc{origin: "model", format: format, data: encodeMock(t, doc, format)}}
		}

		scope, err := m.Mock(builder)
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		contentType, body := formModel{files: slices.Concat(others, files)}.encode(t)

		res, _ := post(t, m.URL("/files"), contentType, body)
		require.Equal(t, want, res.StatusCode == http.StatusOK, "status %d", res.StatusCode)
	})
}

func FuzzFormRequest(f *testing.F) {
	// M27: no content type or body sent to a server holding form mocks panics it or stops the search: the answer is
	// the form mock or the catch-all.
	valid, body := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.png", contentType: "image/png", body: []byte("\x89PNG")}},
	}.encode(f)

	for _, seed := range []struct {
		contentType string
		body        []byte
	}{
		{valid, body},
		{valid, body[:len(body)/2]},
		{valid, body[:len(body)-6]},
		{"multipart/form-data", body},
		{"multipart/form-data; boundary=", body},
		{"multipart/mixed; boundary=x", []byte("--x\r\n\r\nx\r\n--x--\r\n")},
		{MIMEFormURLEncoded, []byte("a=1&a=%zz")},
		{"", nil},
	} {
		f.Add(seed.contentType, seed.body)
	}

	logs := &logBuffer{}
	logger := zerolog.New(logs).Level(zerolog.ErrorLevel)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	f.Cleanup(m.Close)

	m.MustMock(Postf("/form").FormField("a", match.Equal("1")).FormFile("f", FileWith()).Reply(OK().BodyText("form")))
	m.MustMock(Postf("/form").Priority(1).Reply(Accepted().BodyText("catch-all")))

	f.Fuzz(func(t *testing.T, contentType string, body []byte) {
		// A header value can't hold control characters: the client refuses to send it.
		if strings.ContainsFunc(contentType, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return
		}

		_, got := post(t, m.URL("/form"), contentType, body)
		require.Contains(t, []string{"form", "catch-all"}, string(got))
		require.NotContains(t, logs.String(), "panic during request matching")
	})
}
