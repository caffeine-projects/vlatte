package mockhttp

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// readPart is a part of a multipart body as a client reads it.
type readPart struct {
	header map[string][]string
	body   string
}

// readMultipart reads a multipart response body with Go's multipart reader, and returns its media type and parts.
func readMultipart(t require.TestingT, contentType string, body []byte) (string, string, []readPart) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)

	var parts []readPart

	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		b, err := io.ReadAll(part)
		require.NoError(t, err)

		parts = append(parts, readPart{header: part.Header, body: string(b)})
	}

	return mediaType, params["boundary"], parts
}

func TestReplyMultipart_ServesItsParts(t *testing.T) {
	// A multipart reply serves its parts, in order, with their headers, under a Content-Type that names the boundary.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/form").Reply(OK().Multipart(MultipartForm(
		NewPart().Name("title").BodyText("Holiday"),
		NewPart().Name("meta").BodyJSON(map[string]any{"id": 1}),
		NewPart().Name("avatar").Filename("a.png").Body([]byte("\x89PNG\r\n--")),
		NewPart().Name("note").ContentType("text/plain; charset=utf-8").Header("X-A", "1").BodyText("hi"),
	))))

	res, body := fetch(t, m.URL("/form"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	mediaType, _, parts := readMultipart(t, res.Header.Get(HeaderContentType), body)
	require.Equal(t, "multipart/form-data", mediaType)
	require.Equal(t, []readPart{
		{header: map[string][]string{"Content-Disposition": {`form-data; name="title"`}}, body: "Holiday"},
		{header: map[string][]string{"Content-Disposition": {`form-data; name="meta"`}, "Content-Type": {"application/json"}}, body: `{"id":1}`},
		{header: map[string][]string{"Content-Disposition": {`form-data; name="avatar"; filename="a.png"`}, "Content-Type": {"application/octet-stream"}}, body: "\x89PNG\r\n--"},
		{header: map[string][]string{"Content-Disposition": {`form-data; name="note"`}, "Content-Type": {"text/plain; charset=utf-8"}, "X-A": {"1"}}, body: "hi"},
	}, parts)
}

func TestReplyMultipart_OneBoundaryPerMock(t *testing.T) {
	// Decision 12: a reply draws its boundary once, when the mock is built, so every response is the same bytes. A
	// boundary that is set is the one used.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/random").Reply(OK().Multipart(MultipartMixed(NewPart().BodyText("a")))))
	m.MustMock(Getf("/fixed").Reply(OK().Multipart(MultipartMixed(NewPart().BodyText("a")).Boundary("b0"))))

	first, firstBody := fetch(t, m.URL("/random"))
	second, secondBody := fetch(t, m.URL("/random"))
	require.Equal(t, first.Header.Get(HeaderContentType), second.Header.Get(HeaderContentType))
	require.Equal(t, firstBody, secondBody)

	fixed, fixedBody := fetch(t, m.URL("/fixed"))
	require.Equal(t, "multipart/mixed; boundary=b0", fixed.Header.Get(HeaderContentType))
	require.Equal(t, "--b0\r\n\r\na\r\n--b0--\r\n", string(fixedBody))
}

func TestReplyMultipart_BuildErrors(t *testing.T) {
	// What can't make a multipart body fails the build of the mock, saying why.
	tests := []struct {
		name  string
		reply *Reply
		want  string
	}{
		{"a Content-Type header too", OK().ContentType("text/plain").Multipart(MultipartMixed(NewPart())), "reply: a multipart body sets the Content-Type: remove the Content-Type header"},
		{"a Content-Type header template too", OK().HeaderTemplate("content-type", "{{ .X }}").Multipart(MultipartMixed(NewPart())), "reply: a multipart body sets the Content-Type"},
		{"a form-data part without a name", OK().Multipart(MultipartForm(NewPart().BodyText("x"))), "reply: multipart: parts[0].name is required in a form-data part"},
		{"a JSON body that doesn't marshal", OK().Multipart(MultipartMixed(NewPart().BodyJSON(func() {}))), "reply: multipart: parts[0].body json: "},
		{"no part", OK().Multipart(MultipartMixed()), "reply: multipart: parts is empty"},
		{"a bad boundary", OK().Multipart(MultipartMixed(NewPart()).Boundary("a b ")), "reply: multipart: boundary must be 1 to 70"},
		{"a whole media type", OK().Multipart(NewMultipart("multipart/mixed", NewPart())), "reply: multipart: type must be a subtype"},
		{"a negative delay", OK().Multipart(MultipartMixed(NewPart().Delay(-time.Second))), "reply: multipart: parts[0].delay must be 0 or more"},
	}

	m := MustNew(Setup().Silent())

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(Getf("/x").Reply(tc.reply))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestReplyMultipart_Templates(t *testing.T) {
	// A templated body renders its text with the request and the reply's data; its byte bodies are written as they
	// are.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/upload").Reply(OK().
		SetTemplateData(map[string]any{"id": 7}).
		Multipart(MultipartMixed(
			NewPart().
				Filename(`{{ with .Request.FormFile "avatar" }}{{ .Filename }}{{ end }}`).
				Header("X-Title", `{{ .Request.PostForm.Get "title" }}`).
				BodyText(`id {{ .Extra.id }}`),
			NewPart().Body([]byte("{{ .Extra.id }} as is")),
		).Template())))

	contentType, body := formModel{
		fields: []formField{{name: "title", value: "Holiday"}},
		files:  []formFile{{field: "avatar", filename: "me.png"}},
	}.encode(t)

	res, got := post(t, m.URL("/upload"), contentType, body)
	require.Equal(t, http.StatusOK, res.StatusCode, "%s", got)

	_, _, parts := readMultipart(t, res.Header.Get(HeaderContentType), got)
	require.Len(t, parts, 2)
	require.Equal(t, `attachment; filename="me.png"`, parts[0].header["Content-Disposition"][0])
	require.Equal(t, "Holiday", parts[0].header["X-Title"][0])
	require.Equal(t, "id 7", parts[0].body)
	require.Equal(t, "{{ .Extra.id }} as is", parts[1].body)
}

func TestReplyMultipart_ATemplateThatRendersTheBoundaryFails(t *testing.T) {
	// A part's text can render its boundary, where a reader would end the part. The response fails, naming the part,
	// instead of serving a body that reads back as other parts.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/echo").Reply(OK().Multipart(MultipartMixed(
		NewPart().BodyText(`{{ .Request.URL.Query.Get "q" }}`),
	).Boundary("b").Template())))

	res, _ := fetch(t, m.URL("/echo?q=ok"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body := fetch(t, m.URL("/echo?q=--b"))
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Contains(t, string(body), `multipart: parts[0]: the body holds the boundary "b", so the part would end there`)
}

func TestReplyMultipart_Gzip(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/gzip").Reply(OK().Gzip().Multipart(MultipartMixed(NewPart().BodyText("zipped")))))

	res, body := fetch(t, m.URL("/gzip"))
	require.True(t, res.Uncompressed, "the client gunzipped it")

	_, _, parts := readMultipart(t, res.Header.Get(HeaderContentType), body)
	require.Equal(t, []readPart{{header: make(map[string][]string), body: "zipped"}}, parts)
}

func TestReplyMultipart_InASequence(t *testing.T) {
	// Seq and Rand prepare the replies they hold, so their multipart bodies build too.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/seq").Reply(Seq().
		Add(OK().Multipart(MultipartMixed(NewPart().BodyText("first")))).
		Add(OK().Multipart(MultipartMixed(NewPart().BodyText("second"))))))

	for _, want := range []string{"first", "second"} {
		res, body := fetch(t, m.URL("/seq"))
		_, _, parts := readMultipart(t, res.Header.Get(HeaderContentType), body)
		require.Equal(t, want, parts[0].body)
	}
}

func TestReplyMultipart_LastBodySetterWins(t *testing.T) {
	// D3, for multipart: a multipart body replaces an earlier body, and a later body replaces it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/multipart").Reply(OK().BodyFile("no-such-file").Multipart(MultipartMixed(NewPart().BodyText("part")))))
	m.MustMock(Getf("/text").Reply(OK().Multipart(MultipartMixed(NewPart().BodyText("part"))).BodyText("text")))

	res, body := fetch(t, m.URL("/multipart"))
	_, _, parts := readMultipart(t, res.Header.Get(HeaderContentType), body)
	require.Equal(t, "part", parts[0].body)

	res, body = fetch(t, m.URL("/text"))
	require.NotContains(t, res.Header.Get(HeaderContentType), "multipart", "net/http sniffs a Content-Type for the text")
	require.Equal(t, "text", string(body))

	// Part body setters too.
	_, b, err := MultipartMixed(NewPart().BodyFile("no-such-file").BodyText("text")).Encode(nil)
	require.NoError(t, err)
	require.Contains(t, string(b), "\r\n\r\ntext\r\n")
}

func TestReplyMultipart_Describe(t *testing.T) {
	// The description is a mock file's response.multipart: no generated boundary or Content-Type, and the template
	// switch when the body is templated.
	reply := OK().SetTemplateData(map[string]any{"a": int64(1)}).Multipart(NewMultipart("related",
		NewPart().Header("Content-Id", "<root>").BodyJSON(map[string]any{"id": 1}),
		NewPart().Name("report").Filename("r.pdf").BodyFile("r.pdf").Delay(500_000_000),
	).Param("start", "<root>").Template())

	_, err := MustNew(Setup().Silent()).Mock(Getf("/x").Reply(reply))
	require.NoError(t, err)

	b, err := json.Marshal(reply.Describe().Props, json.Deterministic(true))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"status": 200,
		"multipart": {
			"type": "related",
			"params": {"start": "<root>"},
			"parts": [
				{"header": {"Content-Id": "<root>"}, "body": {"id": 1}},
				{"name": "report", "filename": "r.pdf", "body_file": "r.pdf", "delay": "500ms"}
			]
		},
		"template": {"enabled": true, "data": {"a": 1}}
	}`, string(b))
}

func TestMultipart_Encode(t *testing.T) {
	// Encode writes the body a webhook sends, or a test posts: the same as a reply's.
	contentType, body, err := MultipartForm(NewPart().Name("a").BodyText("1"), NewPart().Name("f").Filename("f.txt").BodyText("x")).Encode(nil)
	require.NoError(t, err)

	r, err := stdlibForm(contentType, body)
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a"))
	require.Equal(t, "f.txt", r.MultipartForm.File["f"][0].Filename)
}

// fetch sends a GET and reads the whole response.
func fetch(t require.TestingT, url string) (*http.Response, []byte) {
	res, err := http.Get(url)
	require.NoError(t, err)

	b, err := readAll(res)
	require.NoError(t, err)

	return res, b
}
