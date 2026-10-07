package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
)

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

func genMultipartReply() *rapid.Generator[multipartReplyModel] {
	return rapid.Custom(func(t *rapid.T) multipartReplyModel {
		m := multipartReplyModel{
			subtype:   rapid.SampledFrom([]string{"form-data", "mixed", "related", "x-mixed-replace"}).Draw(t, "subtype"),
			gzip:      rapid.Bool().Draw(t, "gzip"),
			templated: rapid.Bool().Draw(t, "templated"),
			extra:     genToken().Draw(t, "extra"),
		}

		if rapid.Bool().Draw(t, "has boundary") {
			m.boundary = rapid.StringMatching(`[A-Za-z0-9_]{10,30}`).Draw(t, "boundary")
		}

		for range rapid.IntRange(1, 5).Draw(t, "parts") {
			p := replyPartModel{kind: rapid.SampledFrom([]string{"bytes", "text", "json"}).Draw(t, "kind")}
			if m.subtype == "form-data" || rapid.Bool().Draw(t, "has name") {
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

	rep := OK().SetTemplateData(map[string]any{"v": m.extra}).Multipart(mp)
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
		case m.subtype == "form-data":
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
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		models := rapid.SliceOfN(genMultipartReply(), 1, 2).Draw(t, "replies")

		var reply Replier = models[0].reply()
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
			require.Equal(t, "multipart/"+model.subtype, mediaType)
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
	m := MustNew(Setup().Silent())
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
			response["template"] = map[string]any{"data": map[string]any{"v": reply.extra}}
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
		res, shown := fetch(t, m.URL(m.Config().AdminPath, "mocks", id+"-file"))
		require.Equal(t, http.StatusOK, res.StatusCode, "%s", shown)

		var again map[string]any
		require.NoError(t, json.Unmarshal(shown, &again))
		again["id"] = id + "-again"

		body, err := json.Marshal(again, json.Deterministic(true))
		require.NoError(t, err)

		res, created := post(t, m.URL(m.Config().AdminPath, "mocks"), MIMEApplicationJSON, body)
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
