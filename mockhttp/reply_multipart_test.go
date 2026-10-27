package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
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
	// A part's text can render a line that starts with its boundary, where a reader would end the part. The response
	// fails, naming the part, instead of serving a body that reads back as other parts. Within a line, it's fine.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/echo").Reply(OK().Multipart(MultipartMixed(
		NewPart().BodyText(`{{ .Request.URL.Query.Get "q" }}`),
	).Boundary("b").Template())))

	res, _ := fetch(t, m.URL("/echo?q=a--b"))
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, body := fetch(t, m.URL("/echo?q=--b"))
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Contains(t, string(body), `multipart: parts[0]: a line of the body starts with the boundary "b", which would end the part there`)
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
	_, b, err := MultipartMixed(NewPart().BodyFile("no-such-file").BodyText("text")).Encode(nil, nil)
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
	contentType, body, err := MultipartForm(NewPart().Name("a").BodyText("1"), NewPart().Name("f").Filename("f.txt").BodyText("x")).Encode(nil, nil)
	require.NoError(t, err)

	r, err := stdlibForm(contentType, body)
	require.NoError(t, err)
	require.Equal(t, "1", r.PostForm.Get("a"))
	require.Equal(t, "f.txt", r.MultipartForm.File["f"][0].Filename)
}

func TestMultipart_EncodeCantPause(t *testing.T) {
	// A body written at once, as a webhook or a test sends it, can't pause: a part with a delay is an error, not a
	// delay dropped without a word.
	_, _, err := MultipartMixed(NewPart().BodyText("a"), NewPart().BodyText("b").Delay(time.Second)).Encode(nil, nil)
	require.EqualError(t, err, "multipart: parts[1].delay can't be set: a body written at once doesn't pause; delays apply to replies only")
}

func TestMultipart_PartsAreCopied(t *testing.T) {
	// NewMultipart copies its parts, their bodies too: a caller that reuses its buffer doesn't change the bodies
	// written later, such as a webhook's at each send.
	buf := []byte("first")
	mp := MultipartMixed(NewPart().Body(buf)).Boundary("b")

	copy(buf, "later")

	_, body, err := mp.Encode(nil, nil)
	require.NoError(t, err)
	require.Contains(t, string(body), "\r\n\r\nfirst\r\n")
}

// fetch sends a GET and reads the whole response.
func fetch(t require.TestingT, url string) (*http.Response, []byte) {
	res, err := http.Get(url)
	require.NoError(t, err)

	b, err := readAll(res)
	require.NoError(t, err)

	return res, b
}

// multipartReplyModel is a multipart reply as plain values, and the parts a client reads from it.
type multipartReplyModel struct {
	subtype   string
	boundary  string
	gzip      bool
	templated bool
	extra     string
	parts     []replyPartModel
}

// replyPartModel is a part of a multipart reply model.
type replyPartModel struct {
	name        string
	filename    string
	contentType string
	header      string // X-A, "" for none
	kind        string // bytes, text or json
	body        []byte
}

// reply builds the model with the Go API.
func (m multipartReplyModel) reply() *Reply {
	parts := make([]*Part, len(m.parts))
	for i, p := range m.parts {
		part := NewPart().Name(p.name).Filename(p.filename).ContentType(p.contentType)
		if p.header != "" {
			part.Header("X-A", p.header)
		}

		switch p.kind {
		case "bytes":
			part.Body(p.body)
		case "text":
			part.BodyText(string(p.body))
		default:
			var v any
			if err := json.Unmarshal(p.body, &v); err != nil {
				panic(err)
			}

			part.BodyJSON(v)
		}

		parts[i] = part
	}

	mp := NewMultipart(m.subtype, parts...).Boundary(m.boundary)
	if m.templated {
		mp.Template()
	}

	// Data goes with templates only, as in the model's mock file.
	rep := OK().Multipart(mp)
	if m.templated {
		rep.SetTemplateData(map[string]any{"v": m.extra})
	}

	if m.gzip {
		rep.Gzip()
	}

	return rep
}

// doc is the model as a mock file's response.multipart. Its parts are text or JSON: a file has no byte bodies.
func (m multipartReplyModel) doc() map[string]any {
	parts := make([]any, len(m.parts))
	for i, p := range m.parts {
		part := make(map[string]any)
		for key, v := range map[string]string{"name": p.name, "filename": p.filename, "content_type": p.contentType} {
			if v != "" {
				part[key] = v
			}
		}

		if p.header != "" {
			part["header"] = map[string]any{"X-A": p.header}
		}

		if p.kind == "json" {
			var v any
			if err := json.Unmarshal(p.body, &v); err != nil {
				panic(err)
			}

			part["body"] = v
		} else {
			part["body"] = string(p.body)
		}

		parts[i] = part
	}

	doc := map[string]any{"type": m.subtype, "parts": parts}
	if m.boundary != "" {
		doc["boundary"] = m.boundary
	}

	return doc
}

// want is what a client reads: the parts' headers by the defaults, and their bodies, text rendered when templated.
func (m multipartReplyModel) want() []readPart {
	parts := make([]readPart, len(m.parts))
	for i, p := range m.parts {
		header := make(map[string][]string)

		switch {
		case strings.EqualFold(m.subtype, "form-data"):
			header["Content-Disposition"] = []string{disposition("form-data", p.name, p.filename)}
		case p.name != "" || p.filename != "":
			header["Content-Disposition"] = []string{disposition("attachment", p.name, p.filename)}
		}

		switch {
		case p.contentType != "":
			header["Content-Type"] = []string{p.contentType}
		case p.kind == "json":
			header["Content-Type"] = []string{"application/json"}
		case p.filename != "":
			header["Content-Type"] = []string{"application/octet-stream"}
		}

		if p.header != "" {
			header["X-A"] = []string{p.header}
		}

		body := string(p.body)
		if m.templated && p.kind == "text" {
			body = string(p.body[:len(p.body)-len("{{ .Extra.v }}")]) + m.extra
		}

		parts[i] = readPart{header: header, body: body}
	}

	return parts
}

// disposition is the oracle of a Content-Disposition, for tokens, which need no escaping.
func disposition(kind, name, filename string) string {
	if name != "" {
		kind += `; name="` + name + `"`
	}

	if filename != "" {
		kind += `; filename="` + filename + `"`
	}

	return kind
}

func TestPropReplyMultipart_ServesItsModel(t *testing.T) {
	// M20: a multipart reply serves its model through the server: its subtype, boundary and parts, gzip and
	// templates included, in Seq too. Its responses are the same bytes each time: the boundary is drawn once.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		models := rapid.SliceOfN(genMultipartReply(), 1, 2).Draw(t, "replies")

		var reply ReplierFactory = models[0].reply()
		if len(models) > 1 {
			reply = Seq().Add(models[0].reply()).Add(models[1].reply())
		}

		scope, err := m.Mock(Getf("/multipart").Reply(reply))
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		var first []byte

		for i, model := range models {
			res, body := fetch(t, m.URL("/multipart"))
			require.Equal(t, http.StatusOK, res.StatusCode, "%s", body)
			require.Equal(t, model.gzip, res.Uncompressed, "gzip")

			mediaType, boundary, parts := readMultipart(t, res.Header.Get(HeaderContentType), body)
			require.Equal(t, "multipart/"+strings.ToLower(model.subtype), mediaType)
			require.Equal(t, model.want(), parts, "reply %d", i)

			if model.boundary != "" {
				require.Equal(t, model.boundary, boundary)
			}

			if i == 0 {
				first = body
			}
		}

		if len(models) == 1 {
			_, again := fetch(t, m.URL("/multipart"))
			require.Equal(t, string(first), string(again), "the same bytes each time")
		}
	})
}

// textOnly turns byte bodies into text: a mock file's string body is text.
func (m multipartReplyModel) textOnly() multipartReplyModel {
	m.parts = slices.Clone(m.parts)
	for i := range m.parts {
		if m.parts[i].kind == "bytes" {
			m.parts[i].kind = "text"
		}
	}

	m.gzip = false

	return m
}

// withoutID returns a mock's description as JSON values, without its id.
func withoutID(t require.TestingT, m *Mock) map[string]any {
	var desc map[string]any
	require.NoError(t, json.Unmarshal([]byte(describe(t, m)), &desc))
	delete(desc, "id")

	return desc
}

func TestPropMultipart_GoAndFilesDescribeTheSame(t *testing.T) {
	// M22: a form_file pattern and a multipart reply built with the Go API and from a mock file describe the same,
	// and what the admin API's GET shows posts back as a mock that describes the same again.
	t.Parallel()

	m := MustNew(Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	seq := 0

	rapid.Check(t, func(t *rapid.T) {
		pattern := genFilePatternModel().Draw(t, "pattern")
		if pattern.body != nil && !utf8.ValidString(*pattern.body) {
			pattern.body = new("hello") // a mock file is text
		}

		reply := genMultipartReply().Draw(t, "reply").textOnly()

		seq++
		id := "m22-" + strconv.Itoa(seq)

		// A literal of a file is compared with Equal.
		goPattern := pattern.patternWith(match.Equal)
		goBuilder := Request().ID(id+"-go").Method(http.MethodPost).URL(match.URLPath("/x")).FormFile("f", goPattern).Reply(reply.reply())

		response := map[string]any{"status": int64(200), "multipart": reply.doc()}
		if reply.templated {
			response["template"] = map[string]any{"enabled": true, "data": map[string]any{"v": reply.extra}}
		}

		doc := map[string]any{
			"id":       id + "-file",
			"request":  map[string]any{"method": "POST", "path": "/x", "form_file": map[string]any{"f": pattern.spec()}},
			"response": response,
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		scope, err := m.Mock(goBuilder, docBuilder{mockDoc{origin: "model", format: format, data: encodeMock(t, doc, format)}})
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		fromGo := withoutID(t, scope.Mock(id+"-go"))
		require.Equal(t, fromGo, withoutID(t, scope.Mock(id+"-file")))

		// GET shows the mock; POSTed back under another id, it describes the same.
		res, shown := fetch(t, m.AdminURL()+"/mocks/"+id+"-file")
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", shown)

		var again map[string]any
		require.NoError(t, json.Unmarshal(shown, &again))
		again["id"] = id + "-again"

		body, err := json.Marshal(again, json.Deterministic(true))
		require.NoError(t, err)

		res, created := post(t, m.AdminURL()+"/mocks", MIMEApplicationJSON, body)
		require.Equal(t, http.StatusCreated, res.StatusCode, "%s\n%s", created, body)

		posted, err := m.storage.Pick(context.Background(), id+"-again")
		require.NoError(t, err)

		defer m.storage.Delete(context.Background(), id+"-again")

		require.Equal(t, fromGo, withoutID(t, posted))
	})
}

func TestPropJSONSchema_MultipartPatterns(t *testing.T) {
	// M26: the editor schema's patterns for a multipart type, boundary and parameter name accept a string exactly
	// when the decoder does, through mime.FormatMediaType and multipart.Writer.SetBoundary.
	t.Parallel()

	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		s := rapid.OneOf(rapid.String(), rapid.StringMatching(`[ -~]{0,72}`), rapid.StringMatching(`[A-Za-z0-9'()+_,./:=? -]{1,72}`)).Draw(t, "text")

		multipart := map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "x"}}}

		switch key := rapid.SampledFrom([]string{"type", "boundary", "params"}).Draw(t, "key"); key {
		case "params":
			multipart["params"] = map[string]any{s: "v"}
		default:
			multipart[key] = s
		}

		doc := withRequest(map[string]any{"response": map[string]any{"multipart": multipart}})

		data, err := config.Encode(doc, "json")
		require.NoError(t, err)

		_, decodeErr := mockDoc{origin: "doc", format: "json", data: data}.build(m)
		schemaErr := schema.Validate(instance(t, doc))

		require.Equal(t, decodeErr == nil, schemaErr == nil, "%q\ndecoder: %v\nschema: %v", s, decodeErr, schemaErr)
	})
}

// Generators

func genMultipartReply() *rapid.Generator[multipartReplyModel] {
	return rapid.Custom(func(t *rapid.T) multipartReplyModel {
		m := multipartReplyModel{
			subtype:   rapid.SampledFrom([]string{"form-data", "Form-Data", "mixed", "related", "x-mixed-replace"}).Draw(t, "subtype"),
			gzip:      rapid.Bool().Draw(t, "gzip"),
			templated: rapid.Bool().Draw(t, "templated"),
			extra:     genToken().Draw(t, "extra"),
		}

		if rapid.Bool().Draw(t, "has boundary") {
			m.boundary = rapid.StringMatching(`[A-Za-z0-9_]{10,30}`).Draw(t, "boundary")
		}

		for range rapid.IntRange(1, 5).Draw(t, "parts") {
			p := replyPartModel{kind: rapid.SampledFrom([]string{"bytes", "text", "json"}).Draw(t, "kind")}
			if strings.EqualFold(m.subtype, "form-data") || rapid.Bool().Draw(t, "has name") {
				p.name = genToken().Draw(t, "name")
			}

			if rapid.Bool().Draw(t, "has filename") {
				p.filename = genToken().Draw(t, "filename")
			}

			if rapid.Bool().Draw(t, "has content type") {
				p.contentType = rapid.SampledFrom([]string{"text/plain", "image/png"}).Draw(t, "content type")
			}

			if rapid.Bool().Draw(t, "has header") {
				p.header = genToken().Draw(t, "header")
			}

			switch p.kind {
			case "json":
				p.body, _ = json.Marshal(map[string]any{"v": genToken().Draw(t, "json value")}, json.Deterministic(true))
			default:
				// Bytes hold an action too: only text renders it.
				p.body = []byte(genToken().Draw(t, "body") + "\r\n--{{ .Extra.v }}")
			}

			m.parts = append(m.parts, p)
		}

		return m
	})
}
