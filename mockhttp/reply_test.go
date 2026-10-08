package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _req, _ = http.NewRequest(http.MethodGet, "http://localhost:8080", nil)

func TestReplyFactories(t *testing.T) {
	assert.Equal(t, http.StatusOK, Status(http.StatusOK).response.StatusCode)
	assert.Equal(t, http.StatusOK, OK().response.StatusCode)
	assert.Equal(t, http.StatusCreated, Created().response.StatusCode)
	assert.Equal(t, http.StatusAccepted, Accepted().response.StatusCode)
	assert.Equal(t, http.StatusNoContent, NoContent().response.StatusCode)
	assert.Equal(t, http.StatusPartialContent, PartialContent().response.StatusCode)
	assert.Equal(t, http.StatusMovedPermanently, MovedPermanently("https://nowhere.com.br").response.StatusCode)
	assert.Equal(t, http.StatusNotModified, NotModified().response.StatusCode)
	assert.Equal(t, http.StatusBadRequest, BadRequest().response.StatusCode)
	assert.Equal(t, http.StatusUnauthorized, Unauthorized().response.StatusCode)
	assert.Equal(t, http.StatusForbidden, Forbidden().response.StatusCode)
	assert.Equal(t, http.StatusNotFound, NotFound().response.StatusCode)
	assert.Equal(t, http.StatusMethodNotAllowed, MethodNotAllowed().response.StatusCode)
	assert.Equal(t, http.StatusUnprocessableEntity, UnprocessableEntity().response.StatusCode)
	assert.Equal(t, http.StatusMultipleChoices, MultipleChoices().response.StatusCode)
	assert.Equal(t, http.StatusInternalServerError, InternalServerError().response.StatusCode)
	assert.Equal(t, http.StatusNotImplemented, NotImplemented().response.StatusCode)
	assert.Equal(t, http.StatusBadGateway, BadGateway().response.StatusCode)
	assert.Equal(t, http.StatusServiceUnavailable, ServiceUnavailable().response.StatusCode)
	assert.Equal(t, http.StatusGatewayTimeout, GatewayTimeout().response.StatusCode)
}

func TestReply(t *testing.T) {
	rv := &RequestValues{RawRequest: _req, URL: _req.URL}
	input := ReplyInput{RequestValues: rv, Writer: nil}
	res, err := NewReply().
		Status(http.StatusCreated).
		Header("test", "dev").
		Header("test", "qa").
		Header("hello", "world").
		Cookie(&http.Cookie{Name: "cookie_test"}).
		ExpireCookie(&http.Cookie{Name: "cookie_test_remove"}).
		Body([]byte("hi")).
		Reply(context.Background(), input)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusCreated, res.StatusCode)
	assert.Equal(t, []string{"dev", "qa"}, res.Header.Values("test"))
	assert.Equal(t, "world", res.Header.Get("hello"))
	assert.Equal(t, 2, len(res.Cookies))
	assert.Equal(t, "cookie_test", res.Cookies[0].Name)
	assert.Equal(t, "cookie_test_remove", res.Cookies[1].Name)
	assert.Equal(t, -1, res.Cookies[1].MaxAge)
	assert.Equal(t, "hi", string(res.Body))
}

func TestStdReplyBodyString(t *testing.T) {
	rv := &RequestValues{RawRequest: _req, URL: _req.URL}
	res, err := NewReply().
		Status(http.StatusCreated).
		PlainText("text").
		Reply(context.Background(), ReplyInput{RequestValues: rv, Writer: nil})

	require.NoError(t, err)
	require.Equal(t, "text", string(res.Body))
}

func TestStdReplyBodyJSON(t *testing.T) {
	type jsonData struct {
		Name   string `json:"name"`
		Job    string `json:"job"`
		Active bool   `json:"active"`
	}

	rv := &RequestValues{RawRequest: _req, URL: _req.URL}

	t.Run("should convert struct to json", func(t *testing.T) {
		model := jsonData{
			Name:   "the name",
			Job:    "dev",
			Active: true,
		}

		res, err := NewReply().
			Status(http.StatusCreated).
			BodyJSON(model).
			Reply(context.Background(), ReplyInput{RequestValues: rv, Writer: nil})

		assert.NoError(t, err)

		b := jsonData{}
		err = json.Unmarshal(res.Body, &b)

		assert.Nil(t, err)
		assert.Equal(t, model, b)
	})

	t.Run("should report conversion error", func(t *testing.T) {
		res, err := NewReply().
			Status(http.StatusCreated).
			BodyJSON(make(chan int)).
			Reply(context.Background(), ReplyInput{RequestValues: rv, Writer: nil})

		assert.Nil(t, res)
		assert.NotNil(t, err)
	})
}

func TestStdReplyBodyJSON_StableOutput(t *testing.T) {
	// Clients and snapshot tests compare reply bodies byte by byte, so the encoding must not vary:
	// nil slices encode as [] (encoding/json/v2) and map keys are sorted.
	rv := &RequestValues{RawRequest: _req, URL: _req.URL}
	testCases := []struct {
		name string
		data any
		want string
	}{
		{"nil slice", struct {
			Items []string `json:"items"`
		}{}, `{"items":[]}`},
		{
			"map keys sorted",
			map[string]int{"h": 8, "c": 3, "f": 6, "a": 1, "g": 7, "b": 2, "e": 5, "d": 4},
			`{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewReply().BodyJSON(tc.data).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

			require.NoError(t, err)
			require.Equal(t, tc.want, string(res.Body))
		})
	}
}

func TestStdReplyBodyReader(t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString("hello\n")
	buf.WriteString("world\n")

	rv := &RequestValues{RawRequest: _req, URL: _req.URL}
	res, err := NewReply().
		Status(http.StatusCreated).
		BodyReader(buf).
		Reply(context.Background(), ReplyInput{RequestValues: rv, Writer: nil})

	assert.NoError(t, err)
	assert.Equal(t, "hello\nworld\n", string(res.Body))
}

func TestHeaderTemplate_RendersPerResponse(t *testing.T) {
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(Getf("/header").Reply(OK().HeaderTemplate("x-method", "{{ .Request.Method }}")))

	// Rendered values belong to one response: they must not pile up on the reply across requests.
	for range 2 {
		res, err := http.Get(m.URL("/header"))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, []string{"GET"}, res.Header.Values("x-method"))
	}
}

func TestReply_TheLastBodySetterWins(t *testing.T) {
	// D3: body setters didn't reset the body kind, so BodyFile(x).BodyText(y) served the file, and
	// BodyTemplate(x).BodyFile(f) served the file as a template. Flags were added with +=, so BodyTemplate twice set
	// the filename flag instead and left the template unparsed.
	file := filepath.Join(t.TempDir(), "body.txt")
	require.NoError(t, os.WriteFile(file, []byte("file {{ .Request.Method }}"), 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		name  string
		reply *Reply
		want  string
	}{
		{"file then text", OK().BodyFile(file).BodyText("text"), "text"},
		{"text then file", OK().BodyText("text").BodyFile(file), "file {{ .Request.Method }}"},
		{"template then file", OK().BodyTemplate("t {{ .Request.Method }}").BodyFile(file), "file {{ .Request.Method }}"},
		{"templated file then bytes", OK().BodyFileWithTemplate(file).Body([]byte("bytes")), "bytes"},
		{"file then templated file", OK().BodyFile(file).BodyFileWithTemplate(file), "file GET"},
		{"template twice", OK().BodyTemplate("a").BodyTemplate("b {{ .Request.Method }}"), "b GET"},
		{"failed JSON then text", OK().BodyJSON(func() {}).BodyText("text"), "text"},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.MustMock(Getf("/%d", i).Reply(tc.reply))

			res, err := http.Get(m.URL("/" + strconv.Itoa(i)))
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tc.want, string(b))
		})
	}
}

func TestReply_DescribesTheBodyInForce(t *testing.T) {
	// D3: Describe showed a body_file an earlier setter left. D12: it never showed the content of a template, so admin
	// GET output of a templated reply didn't load back.
	tests := []struct {
		name  string
		reply *Reply
		want  map[string]any
	}{
		{"file then text", OK().BodyFile("f.txt").BodyText("text"), map[string]any{"status": 200, "body": "text"}},
		{"text then file", OK().BodyText("text").BodyFile("f.txt"), map[string]any{"status": 200, "body_file": "f.txt"}},
		{
			"template", OK().BodyTemplate("t {{ .X }}"),
			map[string]any{"status": 200, "body": "t {{ .X }}", "template": map[string]any{"enabled": true}},
		},
		{
			"templated file", OK().BodyFileWithTemplate("f.txt"),
			map[string]any{"status": 200, "body_file": "f.txt", "template": map[string]any{"enabled": true}},
		},
		{
			// Header templates read the data too, but the body stays text.
			"header templates and data", OK().HeaderTemplate("X-Name", "{{ .Extra.name }}").SetTemplateData(map[string]any{"name": "a"}).BodyText("{{ text }}"),
			map[string]any{
				"status":          200,
				"header_template": http.Header{"X-Name": {"{{ .Extra.name }}"}},
				"body":            "{{ text }}",
				"template":        map[string]any{"data": map[string]any{"name": "a"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.reply.Describe().Props)
		})
	}
}

func TestReply_DescriptionLoadsBack(t *testing.T) {
	// The admin API shows a mock with Describe and loads it back: the header templates, the data they read, and a
	// body that is text stay as they were.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	reply := OK().
		HeaderTemplate("X-Name", "{{ .Extra.name }}").
		SetTemplateData(map[string]any{"name": "ana"}).
		BodyText("{{ not a template }}")

	m.MustMock(Getf("/a").Reply(reply))

	doc, err := json.Marshal(map[string]any{"request": map[string]any{"path": "/b"}, "response": reply.Describe().Props})
	require.NoError(t, err)

	m.MustMock(FromBytes(doc, "json"))

	for _, path := range []string{"/a", "/b"} {
		res, body := fetch(t, m.URL(path))
		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, "ana", res.Header.Get("X-Name"), path)
		require.Equal(t, "{{ not a template }}", string(body), path)
	}
}
