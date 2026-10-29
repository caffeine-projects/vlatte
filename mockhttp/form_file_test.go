package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestFormFile_Conditions(t *testing.T) {
	// Decision 4: a pattern is what one file of a field must have: each condition reads one thing of the file, as
	// net/http parsed it.
	fixture := filepath.Join(t.TempDir(), "avatar.png")
	require.NoError(t, os.WriteFile(fixture, []byte("\x89PNG\r\n--"), 0o600))

	png := formFile{field: "f", filename: "a.png", contentType: "image/PNG; name=x", body: []byte("\x89PNG\r\n--")}
	plain := formFile{field: "f", filename: "b.txt", body: []byte("hello")}
	empty := formFile{field: "f", filename: "e.txt", contentType: "text/plain"}
	jsonFile := formFile{field: "f", filename: "d.json", contentType: "application/json", body: []byte(`{"id": 1}`)}

	tests := []struct {
		name    string
		pattern *FilePattern
		files   []formFile
		status  int
	}{
		{"no condition", FileWith(), []formFile{plain}, http.StatusOK},
		{"filename", FileWith().Filename(match.Equal("a.png")), []formFile{png}, http.StatusOK},
		{"another filename", FileWith().Filename(match.Equal("b.png")), []formFile{png}, StatusNoMatch},
		{"media type, lower case, no parameters", FileWith().ContentType(match.Equal("image/png")), []formFile{png}, http.StatusOK},
		{"no content type reads empty", FileWith().ContentType(match.Equal("")), []formFile{plain}, http.StatusOK},
		{"no content type isn't present", FileWith().ContentType(match.Present()), []formFile{plain}, StatusNoMatch},
		{"header, any case", FileWith().Header("content-type", match.HasPrefix("image/PNG")), []formFile{png}, http.StatusOK},
		{"body", FileWith().Body(match.Equal("hello")), []formFile{plain}, http.StatusOK},
		{"another body", FileWith().Body(match.Equal("bye")), []formFile{plain}, StatusNoMatch},
		{"body matchers all match", FileWith().Body(match.HasPrefix("he"), match.HasSuffix("lo")), []formFile{plain}, http.StatusOK},
		{"JSON body", FileWith().Body(match.JSONPath("$.id", match.Equal(1))), []formFile{jsonFile}, http.StatusOK},
		{"empty body reads empty", FileWith().Body(match.Equal("")), []formFile{empty}, http.StatusOK},
		{"empty body isn't present", FileWith().Body(match.Present()), []formFile{empty}, StatusNoMatch},
		{"body file", FileWith().BodyFile(fixture), []formFile{png}, http.StatusOK},
		{"another body file", FileWith().BodyFile(fixture), []formFile{plain}, StatusNoMatch},
		{"every condition", FileWith().Filename(match.Equal("a.png")).ContentType(match.Equal("text/plain")), []formFile{png}, StatusNoMatch},
		{"some file", FileWith().ContentType(match.Equal("image/png")), []formFile{plain, png}, http.StatusOK},
		{"every file", FileWith().ContentType(match.Equal("image/png")).Every(), []formFile{plain, png}, StatusNoMatch},
		{"every file of one", FileWith().ContentType(match.Equal("image/png")).Every(), []formFile{png}, http.StatusOK},
		{"no file", FileWith(), nil, StatusNoMatch},
		{"no file, every", FileWith().Every(), nil, StatusNoMatch},
		{"a file of another field", FileWith(), []formFile{{field: "g", filename: "a.png"}}, StatusNoMatch},
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.MustMock(Postf("/%d", i).FormFile("f", tc.pattern).Reply(OK()))

			contentType, body := formModel{fields: []formField{{name: "x", value: "1"}}, files: tc.files}.encode(t)

			res, got := post(t, m.URL("/"+strconv.Itoa(i)), contentType, body)
			require.Equal(t, tc.status, res.StatusCode, "%s", got)
		})
	}
}

func TestFormFile_EachPatternNeedsAFile(t *testing.T) {
	// Two patterns of one field are both needed: each is met by some file.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/files").
		FormFile("f", FileWith().Filename(match.Equal("a.png"))).
		FormFile("f", FileWith().Filename(match.Equal("b.png"))).
		Reply(OK()))

	both, bothBody := formModel{files: []formFile{{field: "f", filename: "b.png"}, {field: "f", filename: "a.png"}}}.encode(t)
	one, oneBody := formModel{files: []formFile{{field: "f", filename: "a.png"}}}.encode(t)

	res, _ := post(t, m.URL("/files"), both, bothBody)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, _ = post(t, m.URL("/files"), one, oneBody)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestFormFile_NotAMultipartForm(t *testing.T) {
	// A urlencoded form has no files, whatever its fields say.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/files").FormFile("f", FileWith()).Reply(OK()))

	res, _ := post(t, m.URL("/files"), MIMEFormURLEncoded, []byte("f=a.png"))
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestFormFile_FilenamesArriveAsTheirBase(t *testing.T) {
	// genFilename leaves "/" out: Go keeps the base of a file name (multipart.Part.FileName), so dir/a.png arrives
	// as a.png.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/files").FormFile("f", FileWith().Filename(match.Equal("a.png"))).Reply(OK()))

	contentType, body := formModel{files: []formFile{{field: "f", filename: "dir/a.png"}}}.encode(t)

	res, _ := post(t, m.URL("/files"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestFormFile_ABodyFileThatCantBeReadFailsTheBuild(t *testing.T) {
	m := MustNew(Setup().Silent())

	_, err := m.Mock(Postf("/files").FormFile("avatar", FileWith().BodyFile("testdata/no-such-file.png")).Reply(OK()))
	require.ErrorContains(t, err, "mock: form_file avatar: open testdata/no-such-file.png: ")
}

func TestFormFile_PatternsAreCopied(t *testing.T) {
	// FormFile copies the pattern: changing it later doesn't change the mock.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	p := FileWith().Header("x-a", match.Equal("1"))
	m.MustMock(Postf("/files").FormFile("f", p).Reply(OK()))
	p.Header("x-b", match.Equal("2")).Every()

	withA := map[string]string{"X-A": "1"}
	contentType, body := formModel{files: []formFile{{field: "f", filename: "a", header: withA}, {field: "f", filename: "b", header: withA}}}.encode(t)

	res, _ := post(t, m.URL("/files"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestFormFile_Describe(t *testing.T) {
	// The description is a mock file's form_file: one pattern of a field is an object, several a list, in order. An
	// empty pattern, any file, is an empty object.
	fixture := filepath.Join(t.TempDir(), "a.png")
	require.NoError(t, os.WriteFile(fixture, []byte("png"), 0o600))

	b := Postf("/files").
		FormFile("avatar", FileWith().
			Filename(match.HasSuffix(".png")).
			ContentType(match.Equal("image/png")).
			Header("X-Checksum", match.Present()).
			BodyFile(fixture).
			Every()).
		FormFile("docs", FileWith().Filename(match.Equal("a.txt"))).
		FormFile("docs", FileWith().Body(match.Contain("x"))).
		FormFile("docs", FileWith()).
		Reply(OK())

	mock, err := b.Build(MustNew(Setup().Silent()))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(describe(t, mock)), &got))

	request, _ := got["request"].(map[string]any)
	require.Equal(t, map[string]any{
		"avatar": map[string]any{
			"filename":     []any{"hasSuffix", ".png"},
			"content_type": []any{"eq", "image/png"},
			"header":       map[string]any{"X-Checksum": []any{"present"}},
			"body_file":    fixture,
			"every":        true,
		},
		"docs": []any{
			map[string]any{"filename": []any{"eq", "a.txt"}},
			map[string]any{"body": []any{"contains", "x"}},
			make(map[string]any),
		},
	}, request["form_file"])
}

func TestFormFile_AFileThatCantBeReadFailsTheMatch(t *testing.T) {
	// A file that can't be read, such as a temporary file already removed, fails the match and says why, rather than
	// passing for an empty file.
	r := goneFiles(t, formModel{files: []formFile{{field: "f", filename: "a.txt", body: []byte("hello")}}})

	m := &fileMatcher{pattern: *FileWith().Body(match.StrictEqual("hello"))}
	ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), r.MultipartForm.File["f"]))
	require.False(t, ok)
	require.ErrorContains(t, err, "form_file: reading a.txt: ")
}

func TestFormFile_MatcherErrorsStopTheSearch(t *testing.T) {
	// A matcher of user code that fails stops the search, as anywhere else, naming the condition.
	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger))
	m.MustStart()
	t.Cleanup(m.Close)

	boom := match.Function(func(any) (bool, error) { return false, errors.New("boom") })
	m.MustMock(Postf("/filename").FormFile("f", FileWith().Filename(boom)).Reply(OK()))
	m.MustMock(Postf("/body").FormFile("f", FileWith().Body(boom)).Reply(OK()))

	contentType, body := formModel{files: []formFile{{field: "f", filename: "a"}}}.encode(t)

	for path, want := range map[string]string{"/filename": "form_file filename: ", "/body": "form_file body: "} {
		res, _ := post(t, m.URL(path), contentType, body)
		require.Equal(t, StatusNoMatch, res.StatusCode)
		require.Contains(t, logs.String(), want)
	}
}

func TestFormFile_BodyNeedsAMatcher(t *testing.T) {
	require.PanicsWithValue(t, "form_file: Body needs at least one matcher", func() { FileWith().Body() })
}

func TestFormFile_DescribesUnknownMatchersAsAnything(t *testing.T) {
	// A matcher that can't describe itself shows as anything, as in the match package.
	mock, err := Postf("/files").
		FormFile("f", FileWith().Filename(vlatte.MatcherFunc("custom", func(vlatte.MatcherArg) (bool, error) { return true, nil }))).
		Reply(OK()).
		Build(MustNew(Setup().Silent()))
	require.NoError(t, err)
	require.Contains(t, describe(t, mock), `"form_file":{"f":{"filename":["anything"]}}`)
}

// filePatternModel is a FilePattern as plain values, with the oracle that says which files it matches.
type filePatternModel struct {
	filename    *string
	contentType *string
	header      *string // X-A
	body        *string
	every       bool
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
	t.Parallel()

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

// Generators

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
