package mockhttp

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// formModel is a multipart form, in the order its parts are sent: text fields, then files.
type formModel struct {
	fields []formField
	files  []formFile
}

type formField struct {
	name  string
	value string
}

type formFile struct {
	field       string
	filename    string
	contentType string            // "" sends no Content-Type
	header      map[string]string // more headers of the part
	body        []byte
}

// _genFileTypes are the Content-Type values files are sent with, "" for none.
var _genFileTypes = []string{"", "image/png", "text/plain; charset=utf-8", "Application/JSON", "application/octet-stream"}

// genFormName draws a field name with the characters a Content-Disposition quotes or escapes.
// CR and LF are left out: Go writes them as %0D and %0A, so such a name doesn't arrive as written.
// TestForms_LineBreaksInNamesArriveEscaped pins it.
func genFormName() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-\[\]"\\;=éü]{1,8}`)
}

// genFilename draws a file name. "/" is left out: Go keeps the base of a file name, so a/b arrives as b.
// TestFormFile_FilenamesArriveAsTheirBase pins it.
func genFilename() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-"\\;=éü]{1,10}`)
}

// genFileBody draws a file's content: empty, text, or bytes with line breaks and dashes, as a boundary has.
func genFileBody() *rapid.Generator[[]byte] {
	return rapid.OneOf(
		rapid.Just([]byte{}),
		rapid.Map(rapid.String(), func(s string) []byte { return []byte(s) }),
		rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"\r\n", "--", "\r\n--", "\x00", "\xff", "a"}), 1, 12),
			func(chunks []string) []byte {
				var b bytes.Buffer
				for _, c := range chunks {
					b.WriteString(c)
				}

				return b.Bytes()
			}),
	)
}

func genForm() *rapid.Generator[formModel] {
	return rapid.Custom(func(t *rapid.T) formModel {
		var f formModel
		for range rapid.IntRange(0, 3).Draw(t, "fields") {
			f.fields = append(f.fields, formField{
				name:  genFormName().Draw(t, "field name"),
				value: rapid.String().Draw(t, "field value"),
			})
		}

		for range rapid.IntRange(0, 3).Draw(t, "files") {
			f.files = append(f.files, formFile{
				field:       genFormName().Draw(t, "file field"),
				filename:    genFilename().Draw(t, "filename"),
				contentType: rapid.SampledFrom(_genFileTypes).Draw(t, "file content type"),
				body:        genFileBody().Draw(t, "file body"),
			})
		}

		return f
	})
}

// encode writes the form with Go's multipart writer, under a fresh random boundary.
func (f formModel) encode(t require.TestingT) (contentType string, body []byte) {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	for _, field := range f.fields {
		require.NoError(t, w.WriteField(field.name, field.value))
	}

	for _, file := range f.files {
		h := make(textproto.MIMEHeader)
		h.Set(HeaderContentDisposition, multipart.FileContentDisposition(file.field, file.filename))

		if file.contentType != "" {
			h.Set(HeaderContentType, file.contentType)
		}

		for k, v := range file.header {
			h.Set(k, v)
		}

		part, err := w.CreatePart(h)
		require.NoError(t, err)

		_, err = part.Write(file.body)
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())

	return w.FormDataContentType(), buf.Bytes()
}

// stdlibForm parses a body as net/http does: the oracle of what a server sees.
func stdlibForm(contentType string, body []byte) (*http.Request, error) {
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set(HeaderContentType, contentType)

	return r, r.ParseMultipartForm(int64(len(body)))
}

// goneFiles parses a form to temporary files and removes them, as net/http does once the request is done: its files
// can't be opened any more.
func goneFiles(t require.TestingT, form formModel) *http.Request {
	contentType, body := form.encode(t)

	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	r.Header.Set(HeaderContentType, contentType)
	require.NoError(t, r.ParseMultipartForm(0)) // no memory for files: each goes to a temporary file
	require.NoError(t, r.MultipartForm.RemoveAll())

	return r
}

// post sends a body and reads the whole response.
func post(t require.TestingT, url, contentType string, body []byte) (*http.Response, []byte) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set(HeaderContentType, contentType)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := readAll(res)
	require.NoError(t, err)

	return res, b
}

func readAll(res *http.Response) ([]byte, error) {
	defer res.Body.Close()

	var b bytes.Buffer
	_, err := b.ReadFrom(res.Body)

	return b.Bytes(), err
}
