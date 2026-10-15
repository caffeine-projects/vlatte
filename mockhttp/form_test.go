package mockhttp

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

func TestForms_FormFieldReadsMultipartFields(t *testing.T) {
	// Decision 2: FormField reads the text fields of the body's form, urlencoded or multipart, as
	// http.Request.PostFormValue does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/form").FormField("title", match.StrictEqual("Holiday")).Reply(OK()))

	holiday, holidayBody := formModel{fields: []formField{{name: "title", value: "Holiday"}}}.encode(t)
	other, otherBody := formModel{fields: []formField{{name: "title", value: "Other"}}}.encode(t)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		status      int
	}{
		{"multipart", holiday, holidayBody, http.StatusOK},
		{"urlencoded", MIMEFormURLEncoded, []byte("title=Holiday"), http.StatusOK},
		{"multipart, another value", other, otherBody, StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := post(t, m.URL("/form"), tc.contentType, tc.body)
			require.Equal(t, tc.status, res.StatusCode)
		})
	}

	scoped.AssertNumberOfCalls(t, 2)
}

func TestForms_TheQueryIsNotAMultipartField(t *testing.T) {
	// Like TestMockFile_FormFieldsComeFromTheBody: http.Request.Form would mix the query in; FormField reads only
	// the body's form.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/form").FormField("a", match.StrictEqual("1")).Reply(OK()))

	contentType, body := formModel{fields: []formField{{name: "b", value: "2"}}}.encode(t)

	res, _ := post(t, m.URL("/form?a=1"), contentType, body)
	require.Equal(t, StatusNoMatch, res.StatusCode)
}

func TestForms_LineBreaksInNamesArriveEscaped(t *testing.T) {
	// genFormName leaves CR and LF out: Go's multipart writer escapes them as %0D and %0A, and nothing unescapes
	// them, so the server sees the escaped name.
	contentType, body := formModel{fields: []formField{{name: "a\r\nb", value: "1"}}}.encode(t)

	r, err := stdlibForm(contentType, body)
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a%0D%0Ab"))
}

func TestForms_ForwardAndEchoSendTheWholeBody(t *testing.T) {
	// D6: parsing a form reads r.Body to the end, and the proxy, forward and echo replies send r.Body, so they sent
	// an empty body. Both parsers put the body back.
	got := make(chan []byte, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/forward").Reply(From(target.URL)))
	m.MustMock(Postf("/echo").Reply(Echo()))

	// The proxy sends a request that no mock matches to its own URL.
	proxyURL, err := url.Parse(m.URL())
	require.NoError(t, err)

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	t.Cleanup(transport.CloseIdleConnections)

	viaProxy := &http.Client{Transport: transport}

	multipartType, multipartBody := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.bin", body: []byte("\x00\r\n--")}},
	}.encode(t)

	bodies := []struct {
		name        string
		contentType string
		body        []byte
	}{
		{"urlencoded", MIMEFormURLEncoded, []byte("a=1&b=2")},
		{"multipart", multipartType, multipartBody},
	}

	for _, b := range bodies {
		t.Run(b.name+" forward", func(t *testing.T) {
			res, _ := post(t, m.URL("/forward"), b.contentType, b.body)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, b.body, <-got)
		})

		t.Run(b.name+" echo", func(t *testing.T) {
			res, echoed := post(t, m.URL("/echo"), b.contentType, b.body)
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.True(t, bytes.HasSuffix(echoed, b.body), "the echo ends with the whole body: %q", echoed)
		})

		t.Run(b.name+" proxy", func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, target.URL+"/upload", bytes.NewReader(b.body))
			require.NoError(t, err)
			req.Header.Set(HeaderContentType, b.contentType)

			res, err := viaProxy.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, b.body, <-got)
		})
	}
}

func TestForms_ACutFormKeepsNoField(t *testing.T) {
	// D8: MaxBodyParsingLimit cuts a body without a word. Go's ReadForm takes a body cut right after a delimiter
	// line, or inside the headers of a part, for a whole one, and keeps the fields before the cut; one cut inside a
	// part's content fails. Either way a cut form keeps no field, so no form mock matches, and a warning says why.
	contentType, body := formModel{
		fields: []formField{{name: "a", value: "1"}},
		files:  []formFile{{field: "f", filename: "f.bin", body: bytes.Repeat([]byte("x"), 1024)}},
	}.encode(t)

	second := bytes.Index(body[2:], []byte("--")) + 2 // the delimiter line before the file part

	cuts := []struct {
		name string
		at   int
	}{
		{"after a delimiter line", bytes.Index(body[second:], []byte("\r\n")) + second + 2},
		{"inside the headers of a part", bytes.Index(body, []byte("filename"))},
		{"inside the content of a part", len(body) - 100},
	}

	for _, cut := range cuts {
		t.Run(cut.name, func(t *testing.T) {
			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).MaxBodyParsingLimit(int64(cut.at)))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Postf("/form").FormField("a", match.StrictEqual("1")).Reply(OK()))

			res, _ := post(t, m.URL("/form"), contentType, body)
			require.Equal(t, StatusNoMatch, res.StatusCode)
			require.Contains(t, logs.String(), "request_body_parser")
		})
	}

	// The cuts ReadForm takes for whole bodies.
	r, err := stdlibForm(contentType, body[:cuts[0].at])
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a"), "Go keeps the field before a cut after a delimiter line")
}

func TestForms_ACutFormWithItsDelimiterInAPart(t *testing.T) {
	// The content of a part may hold "--b--": within a line it isn't a delimiter, and Go's reader of a CRLF body
	// skips it after a bare LF too. A body cut after it, inside the headers of a later part, is still cut. A body
	// shorter than its Content-Length is cut; without a length, a body is whole only when the close delimiter starts a
	// line. Either way it keeps no field.
	tests := []struct {
		name    string
		value   string
		chunked bool
	}{
		{"within a line", "x--b--y", false},
		{"within a line, chunked", "x--b--y", true},
		{"after a bare LF", "x\n--b--\ny", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			w := multipart.NewWriter(&buf)
			require.NoError(t, w.SetBoundary("b"))
			require.NoError(t, w.WriteField("a", tc.value))

			fw, err := w.CreateFormFile("f", "f.bin")
			require.NoError(t, err)

			_, err = fw.Write([]byte("file"))
			require.NoError(t, err)
			require.NoError(t, w.Close())

			body := buf.Bytes()
			cut := bytes.Index(body, []byte("filename")) // inside the headers of the file part

			// Go keeps the field before such a cut.
			r, err := stdlibForm(w.FormDataContentType(), body[:cut])
			require.NoError(t, err)
			require.Equal(t, tc.value, r.PostForm.Get("a"))

			logs := &logBuffer{}
			logger := zerolog.New(logs)

			m := MustNew(Setup().Logger(&logger).MaxBodyParsingLimit(int64(cut)))
			m.MustStart()
			t.Cleanup(m.Close)

			m.MustMock(Postf("/form").FormField("a", match.StrictEqual(tc.value)).Reply(OK()))

			var reader io.Reader = bytes.NewReader(body)
			if tc.chunked {
				reader = io.MultiReader(reader) // the client can't know the length, so it sends the body chunked
			}

			req, err := http.NewRequest(http.MethodPost, m.URL("/form"), reader)
			require.NoError(t, err)
			req.Header.Set(HeaderContentType, w.FormDataContentType())

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, StatusNoMatch, res.StatusCode)
			require.Contains(t, logs.String(), "request_body_parser")
		})
	}
}

func TestForms_ABoundaryThatNamesAMediaType(t *testing.T) {
	// A boundary may hold a media type, quoted. The text and urlencoded parsers took such a form for theirs when they
	// read the Content-Type as text, and it had no fields and no files.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	scoped := m.MustMock(Postf("/upload").
		FormField("title", match.StrictEqual("Holiday")).
		FormFile("photo", FileWith().Filename(match.Equal("a.png"))).
		Reply(OK()))

	boundaries := []string{MIMEApplicationJSON, MIMETextPlain, MIMEFormURLEncoded}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			var body bytes.Buffer

			w := multipart.NewWriter(&body)
			require.NoError(t, w.SetBoundary(boundary))
			require.NoError(t, w.WriteField("title", "Holiday"))

			fw, err := w.CreateFormFile("photo", "a.png")
			require.NoError(t, err)

			_, err = fw.Write([]byte("PNG"))
			require.NoError(t, err)
			require.NoError(t, w.Close())

			res, _ := post(t, m.URL("/upload"), w.FormDataContentType(), body.Bytes())
			require.Equal(t, http.StatusOK, res.StatusCode, "Content-Type: %s", w.FormDataContentType())
		})
	}

	scoped.AssertNumberOfCalls(t, int64(len(boundaries)))
}

func TestBuilder_ContentTypeIgnoresParameters(t *testing.T) {
	// D5: ContentType compared the whole header, and a multipart Content-Type always carries a boundary, so
	// ContentType("multipart/form-data") never matched. A value without parameters matches the media type alone.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/multipart").ContentType(MIMEMultipartForm).Reply(OK()))
	m.MustMock(Postf("/json").ContentType(MIMEApplicationJSON).Reply(OK()))
	m.MustMock(Postf("/charset").ContentType("text/plain; charset=utf-8").Reply(OK()))

	multipartType, multipartBody := formModel{fields: []formField{{name: "a", value: "1"}}}.encode(t)

	tests := []struct {
		path        string
		contentType string
		status      int
	}{
		{"/multipart", multipartType, http.StatusOK},
		{"/json", "Application/JSON; charset=utf-8", http.StatusOK},
		{"/json", " application/json ", http.StatusOK},
		{"/json", "application/jsonx", StatusNoMatch},
		{"/json", "", StatusNoMatch},
		{"/charset", "TEXT/plain; charset=UTF-8", http.StatusOK},
		{"/charset", "text/plain", StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.path+" "+tc.contentType, func(t *testing.T) {
			res, _ := post(t, m.URL(tc.path), tc.contentType, multipartBody)
			require.Equal(t, tc.status, res.StatusCode)
		})
	}
}

func TestForms_TemplatesWithoutAForm(t *testing.T) {
	// A request without a form reads as one without fields or files: templates don't fail on it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/no-form").Reply(OK().BodyTemplate(`[{{ .Request.PostForm.Get "a" }}][{{ with .Request.FormFile "f" }}{{ .Filename }}{{ end }}]`)))

	res, body := fetch(t, m.URL("/no-form"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "[][]", string(body))
}
