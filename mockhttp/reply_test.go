package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

var _req, _ = http.NewRequest(http.MethodGet, "http://localhost:8080", nil)

// replierOf makes the Replier that a mock built with f would serve, on a server with default settings.
func replierOf(t *testing.T, f ReplierFactory) Replier {
	t.Helper()

	r, err := f.NewReplier(MustNew(Setup().Silent()))
	require.NoError(t, err)

	return r
}

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
	rep := NewReply().
		Status(http.StatusCreated).
		Header("test", "dev").
		Header("test", "qa").
		Header("hello", "world").
		Cookie(&http.Cookie{Name: "cookie_test"}).
		ExpireCookie(&http.Cookie{Name: "cookie_test_remove"}).
		Body([]byte("hi"))

	res, err := replierOf(t, rep).Reply(t.Context(), input)

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
	rep := NewReply().Status(http.StatusCreated).PlainText("text")
	res, err := replierOf(t, rep).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

	require.NoError(t, err)
	require.Equal(t, "text", string(res.Body))
}

func TestReply_HeaderAndTrailerArraysKeepEveryValueInOrder(t *testing.T) {
	rv := &RequestValues{RawRequest: _req, URL: _req.URL}
	rep := OK().HeaderArr("X-Tag", "a", "b").
		HeaderArrTemplate("X-Path", `{{ "rendered" }}`, "fixed").
		TrailerArr("X-Result", "one", "two")

	res, err := replierOf(t, rep).Reply(t.Context(), ReplyInput{RequestValues: rv})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, res.Header.Values("X-Tag"))
	require.Equal(t, []string{"rendered", "fixed"}, res.Header.Values("X-Path"))
	require.Equal(t, []string{"one", "two"}, res.Trailer.Values("X-Result"))
}

func TestReply_ABodyReadFailureFailsTheBuild(t *testing.T) {
	errRead := errors.New("test: body unavailable")
	replier, err := OK().BodyReader(iotest.ErrReader(errRead)).NewReplier(MustNew(Setup().Silent()))
	require.ErrorIs(t, err, errRead)
	require.Nil(t, replier)
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

		rep := NewReply().Status(http.StatusCreated).BodyJSON(model)
		res, err := replierOf(t, rep).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

		assert.NoError(t, err)

		b := jsonData{}
		err = json.Unmarshal(res.Body, &b)

		assert.Nil(t, err)
		assert.Equal(t, model, b)
	})

	t.Run("should report conversion error when the mock is built", func(t *testing.T) {
		r, err := NewReply().Status(http.StatusCreated).BodyJSON(make(chan int)).NewReplier(MustNew(Setup().Silent()))

		assert.Nil(t, r)
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
			res, err := replierOf(t, NewReply().BodyJSON(tc.data)).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

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
	rep := NewReply().Status(http.StatusCreated).BodyReader(buf)
	res, err := replierOf(t, rep).Reply(t.Context(), ReplyInput{RequestValues: rv, Writer: nil})

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

func TestReply_GzipLeavesTheReplyAsItWas(t *testing.T) {
	// A Reply serves many mocks: each Replier made from it sets the Content-Encoding once, the Reply itself gets none,
	// and it still describes itself as gzip. Set, not Add: a Content-Encoding written with Header goes in once too.
	for _, rep := range []*Reply{OK().Gzip(), OK().Header(HeaderContentEncoding, "gzip").Gzip()} {
		written := slices.Clone(rep.response.Header.Values(HeaderContentEncoding))

		for range 2 {
			r, ok := replierOf(t, rep).(*responseReplier)
			require.True(t, ok)
			require.Equal(t, []string{"gzip"}, r.response.Header.Values(HeaderContentEncoding))
			require.Equal(t, "gzip", r.response.Encoding)
		}

		require.Equal(t, written, rep.response.Header.Values(HeaderContentEncoding), "the Reply keeps what was written")
		require.Empty(t, rep.response.Encoding)
		require.Equal(t, "gzip", rep.Describe().Props.(map[string]any)["encoding"])
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

func TestReply_ADescribedReplyLoadsBackAndAnswersTheSame(t *testing.T) {
	// #42: the description of a reply with trailers wrote trailers, which mock files don't read, and one with a body
	// that isn't UTF-8 failed to encode: the admin API couldn't show either mock, nor keep it. Loaded back, each
	// description answers the same status, body bytes and trailers.
	tests := []struct {
		name string
		give *Reply
	}{
		{
			name: "trailer",
			give: OK().PlainText("t").Trailer("X-T", "v"),
		},
		{
			name: "binary body",
			give: OK().Header(HeaderContentType, "application/octet-stream").Body([]byte{0xff, 0xfe, 0x00}),
		},
		{
			// Latin-1 text, as a client in another encoding gets it.
			name: "text in another encoding",
			give: OK().Header(HeaderContentType, "text/plain; charset=iso-8859-1").Body([]byte("caf\xe9")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			built := MustNew(Setup().Silent())
			built.MustStart()
			t.Cleanup(built.Close)

			desc, err := EncodeMock(built.MustMock(Getf("/x").Reply(tt.give)).Mocks()[0], "json")
			require.NoError(t, err)

			loaded := MustNew(Setup().Silent())
			loaded.MustStart()
			t.Cleanup(loaded.Close)
			_, err = loaded.Mock(FromBytes(desc, "json"))
			require.NoError(t, err, string(desc))

			want, wantBody := fetch(t, built.URL("/x"))
			got, gotBody := fetch(t, loaded.URL("/x"))

			require.Equal(t, want.StatusCode, got.StatusCode, string(desc))
			require.Equal(t, want.Header.Get(HeaderContentType), got.Header.Get(HeaderContentType), string(desc))
			require.Equal(t, wantBody, gotBody, string(desc))
			require.Equal(t, want.Trailer, got.Trailer, string(desc))
		})
	}
}

func TestReply_SharedReplyKeepsServingWhileMocksBuild(t *testing.T) {
	// #23: building a mock compiled templates into the reply it was given, so a request to one mock could read a
	// template that another build had just reset: 418s, missing headers and data races. Building now only reads the
	// reply, and each mock serves a Replier of its own.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	ok := OK().BodyTemplate(`{{ .Request.Method }}`).HeaderTemplate("X-Id", `{{ .Request.ID }}`)
	m.MustMock(Getf("/a").Reply(ok))

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				res, err := http.Get(m.URL("/a"))
				if !assert.NoError(t, err) {
					return
				}

				body, err := io.ReadAll(res.Body)
				assert.NoError(t, err)
				assert.NoError(t, res.Body.Close())

				assert.Equal(t, http.StatusOK, res.StatusCode)
				assert.Equal(t, "GET", string(body))
				assert.NotEmpty(t, res.Header.Get("X-Id"))
			}
		})
	}

	for i := range 50 {
		m.MustMock(Getf("/b%d", i).Reply(ok))
	}

	wg.Wait()
}

func TestReply_EachServerCompilesWithItsOwnEngine(t *testing.T) {
	// #23: a reply shared by two servers was compiled in place, so the last server to build won: the first server
	// rendered with the template engine of the second, and when a build failed, the first server answered 418.
	newServer := func(shout func(string) string) *Server {
		engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{"shout": shout}))
		s := MustNew(Setup().Silent().TemplateEngine(engine))
		s.MustStart()
		t.Cleanup(s.Close)

		return s
	}

	loud := OK().BodyTemplate(`{{ shout .Request.Method }}`)
	upper, lower := newServer(strings.ToUpper), newServer(strings.ToLower)
	upper.MustMock(Getf("/a").Reply(loud))
	lower.MustMock(Getf("/a").Reply(loud))

	_, err := MustNew(Setup().Silent()).Mock(Getf("/a").Reply(loud))
	require.ErrorContains(t, err, `function "shout" not defined`, "the default engine has no shout")

	_, body := get(t, upper, "/a")
	require.Equal(t, "GET", body)

	_, body = get(t, lower, "/a")
	require.Equal(t, "get", body)
}

func TestReply_ChangesAfterMockReachOnlyLaterMocks(t *testing.T) {
	// A mock serves the Replier made from the reply when the mock was built. Changing the reply afterwards, even while
	// the mock serves, changes only the mocks built afterwards, and the first keeps describing the reply it serves.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rep := OK().PlainText("v1")
	served := m.MustMock(Getf("/v1").Reply(rep)).Mocks()[0]

	var wg sync.WaitGroup
	wg.Go(func() {
		rep.Status(http.StatusInternalServerError).
			Header("X-Later", "yes").
			HeaderTemplate("X-Method", "{{ .Request.Method }}").
			Gzip()
	})

	for range 20 {
		res, body := fetch(t, m.URL("/v1"))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "v1", string(body))
		require.Empty(t, res.Header.Get("X-Later"))
		require.Empty(t, res.Header.Get("X-Method"))
	}

	wg.Wait()

	described := served.Describe().Props.(map[string]any)["response"].(map[string]any)
	require.Equal(t, http.StatusOK, described["status"])
	require.NotContains(t, described, "encoding")

	m.MustMock(Getf("/v2").Reply(rep))

	res, body := fetch(t, m.URL("/v2"))
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	require.Equal(t, "v1", string(body))
	require.Equal(t, "yes", res.Header.Get("X-Later"))
	require.Equal(t, "GET", res.Header.Get("X-Method"))
}

// flaky serves fail on every nth request of its mock, and ok on the others. It is a ReplierFactory of its own that
// holds built-in replies, as Seq and Rand do.
type flaky struct {
	every int64
	ok    ReplierFactory
	fail  ReplierFactory
}

func (f flaky) NewReplier(app *Server) (Replier, error) {
	ok, err := f.ok.NewReplier(app)
	if err != nil {
		return nil, err
	}

	fail, err := f.fail.NewReplier(app)
	if err != nil {
		return nil, err
	}

	return &flakyReplier{every: f.every, ok: ok, fail: fail}, nil
}

type flakyReplier struct {
	every int64
	n     atomic.Int64
	ok    Replier
	fail  Replier
}

func (r *flakyReplier) Reply(ctx context.Context, in ReplyInput) (*Response, error) {
	if r.n.Add(1)%r.every == 0 {
		return r.fail.Reply(ctx, in)
	}

	return r.ok.Reply(ctx, in)
}

func TestReply_CustomFactoryHoldsBuiltInReplies(t *testing.T) {
	// A factory of one's own can hold built-in replies: making their Repliers in its NewReplier compiles their
	// templates with the engine of the server, and each mock gets a count of its own. Before, a custom replier holding
	// a *Reply never had it prepared, so its templates were nil.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	f := flaky{every: 2, ok: OK().BodyTemplate(`{{ .Request.Method }}`), fail: ServiceUnavailable()}
	m.MustMock(Getf("/a").Reply(f), Getf("/b").Reply(f))

	steps := []struct {
		path   string
		status int
		body   string
	}{
		{path: "/a", status: http.StatusOK, body: "GET"},
		{path: "/b", status: http.StatusOK, body: "GET"},
		{path: "/a", status: http.StatusServiceUnavailable},
		{path: "/b", status: http.StatusServiceUnavailable},
	}

	for _, s := range steps {
		status, body := get(t, m, s.path)
		require.Equal(t, s.status, status, s.path)
		require.Equal(t, s.body, body, s.path)
	}
}

func TestReply_EachResponseOwnsWhatMappersChange(t *testing.T) {
	// A response shared the header map and the cookies of its reply, so a mapper that set a header or changed a cookie
	// wrote into the reply: the change showed up again on later responses, requests at the same time raced, and a
	// changed cookie was the very one given to Cookie.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	cookie := &http.Cookie{Name: "reply", Value: "yes"}
	m.MustMock(Getf("/mapped").
		Map(func(_ *RequestValues, res *Response) error {
			res.Header.Add("X-Mapped", "yes")
			res.Cookies[0].Value += "-mapped"
			res.Cookies = append(res.Cookies, &http.Cookie{Name: "mapped", Value: "yes"})

			return nil
		}).
		Reply(OK().Cookie(cookie)))

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				res, err := http.Get(m.URL("/mapped"))
				if !assert.NoError(t, err) {
					return
				}

				assert.NoError(t, res.Body.Close())
				assert.Equal(t, []string{"yes"}, res.Header.Values("X-Mapped"))

				cookies := make(map[string]string)
				for _, c := range res.Cookies() {
					cookies[c.Name] = c.Value
				}

				assert.Equal(t, map[string]string{"reply": "yes-mapped", "mapped": "yes"}, cookies)
			}
		})
	}

	wg.Wait()

	require.Equal(t, "yes", cookie.Value, "the cookie given to Cookie stays as it was")
}

func TestReply_ExpireCookieLeavesTheCookieAsItWas(t *testing.T) {
	// ExpireCookie set MaxAge on the cookie it was given, so it expired that cookie for every reply that held it.
	cookie := &http.Cookie{Name: "session", Value: "a"}
	kept := OK().Cookie(cookie)
	expired := OK().ExpireCookie(cookie)

	require.Zero(t, cookie.MaxAge, "the given cookie stays as it was")
	require.Zero(t, kept.response.Cookies[0].MaxAge)
	require.Equal(t, -1, expired.response.Cookies[0].MaxAge)
}

func TestReply_NilCookieRendersNothing(t *testing.T) {
	// Each response copies the cookies of its reply. A nil cookie renders nothing, so it is kept rather than copied: the
	// build doesn't panic, and the other cookies are served.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/c").Reply(OK().Cookie(nil).Cookie(&http.Cookie{Name: "a", Value: "1"})))

	res, err := http.Get(m.URL("/c"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, "a=1", cookies[0].String())
}

func TestReplyTemplates_CompileErrorsFailTheMock(t *testing.T) {
	// Templates compile when the mock is built, so a broken one fails the mock instead of each request.
	m := MustNew(Setup().Silent())

	tests := []struct {
		name  string
		reply *Reply
		want  string
	}{
		{"body", OK().BodyTemplate("{{ .X"), "reply: template: body:1: unclosed action"},
		{"header", OK().HeaderTemplate("X-A", "{{ .X"), "reply: template: header.X-A:1: unclosed action"},
		{"body file name", OK().BodyFile("{{ .X"), "reply: template: body_file:1: unclosed action"},
		{"multipart", OK().Multipart(MultipartMixed(NewPart().BodyText("{{ .X")).Template()), "reply: multipart: parts[0]: body: template: body:1: unclosed action"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Mock(Getf("/x").Reply(tc.reply))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestReplyTemplates_HeaderValuesCantAddHeaders(t *testing.T) {
	// A header value rendered from the request can't hold a line break: it would end the header there, and the rest
	// would read as another header.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/echo").Reply(OK().HeaderTemplate("X-Echo", `{{ .Request.URL.Query.Get "q" }}`)))

	res, body := fetch(t, m.URL()+"/echo?q=a%0D%0AX-Evil:%201")
	require.Equal(t, StatusNoMatch, res.StatusCode)
	require.Empty(t, res.Header.Get("X-Evil"))
	require.Empty(t, res.Header.Get("X-Echo"))
	require.Contains(t, string(body), "reply: header.X-Echo holds a line break")

	res, _ = fetch(t, m.URL()+"/echo?q=a")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "a", res.Header.Get("X-Echo"))
}

func TestReplyTemplates_HeaderValuesLoseTheLineBreaksAround(t *testing.T) {
	// A YAML block scalar ends its value with a line break. A header value goes without the spaces and line breaks
	// around it, as net/http writes headers, so only a line break inside fails.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/go").Reply(OK().HeaderTemplate("X-A", " {{ .Request.Method }}\n")))
	m.MustMock(FromBytes([]byte("request: {path: /file}\nresponse:\n  status: 200\n  header_template:\n    X-A: >\n      {{ .Request.Method }}\n"), "yaml"))

	for _, path := range []string{"/go", "/file"} {
		res, body := fetch(t, m.URL(path))
		require.Equal(t, http.StatusOK, res.StatusCode, "%s: %s", path, body)
		require.Equal(t, "GET", res.Header.Get("X-A"), path)
	}
}

func TestReplyTemplates_EachReplyHasItsOwnHeaderTemplates(t *testing.T) {
	// With a template set on the server, every reply's header templates used to parse into that one template: the
	// last mock's header won for all mocks. Replies compile their own with the server's engine.
	m := MustNew(Setup().Silent().TemplateEngine(vlatte.MustNewGoTemplateEngine()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/a").Reply(OK().HeaderTemplate("X-Who", "a {{ .Request.Method }}")))
	m.MustMock(Getf("/b").Reply(OK().HeaderTemplate("X-Who", "b {{ .Request.Method }}")))

	for _, who := range []string{"a", "b"} {
		res, _ := fetch(t, m.URL("/"+who))
		require.Equal(t, who+" GET", res.Header.Get("X-Who"))
	}
}

func TestReplyTemplates_ConcurrentRequestsKeepTheirOwnValues(t *testing.T) {
	// Every template site renders per request: concurrent requests each get their own values, under -race.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "content.txt"), []byte(`content {{ .Request.URL.Query.Get "n" }}`), 0o600))

	const requests = 32
	for i := range requests {
		require.NoError(t, os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".txt"), []byte("file "+strconv.Itoa(i)), 0o600))
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	n := `{{ .Request.URL.Query.Get "n" }}`
	m.MustMock(Getf("/body").Reply(OK().BodyTemplate("body " + n)))
	m.MustMock(Getf("/header").Reply(OK().HeaderTemplate("X-N", "header "+n)))
	m.MustMock(Getf("/file").Reply(OK().BodyFile(filepath.Join(dir, n+".txt"))))
	m.MustMock(Getf("/content").Reply(OK().BodyFileWithTemplate(filepath.Join(dir, "content.txt"))))
	m.MustMock(Getf("/multipart").Reply(OK().Multipart(MultipartMixed(NewPart().BodyText("part " + n)).Boundary("b").Template())))

	request := func(path string) (*http.Response, string, bool) {
		res, err := http.Get(m.URL() + path)
		if !assert.NoError(t, err) {
			return nil, "", false
		}

		b, err := io.ReadAll(res.Body)
		assert.NoError(t, err)
		assert.NoError(t, res.Body.Close())

		return res, string(b), assert.Equal(t, http.StatusOK, res.StatusCode, path)
	}

	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			q := "?n=" + strconv.Itoa(i)

			if _, body, ok := request("/body" + q); ok {
				assert.Equal(t, "body "+strconv.Itoa(i), body)
			}

			if res, _, ok := request("/header" + q); ok {
				assert.Equal(t, "header "+strconv.Itoa(i), res.Header.Get("X-N"))
			}

			if _, body, ok := request("/file" + q); ok {
				assert.Equal(t, "file "+strconv.Itoa(i), body)
			}

			if _, body, ok := request("/content" + q); ok {
				assert.Equal(t, "content "+strconv.Itoa(i), body)
			}

			if _, body, ok := request("/multipart" + q); ok {
				assert.Contains(t, body, "\r\n\r\npart "+strconv.Itoa(i)+"\r\n")
			}
		})
	}

	wg.Wait()
}

func TestReplyTemplates_MissingKeysAreErrors(t *testing.T) {
	// A missing map key fails the reply and names the key, instead of writing "<no value>" into the body.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Reply(OK().SetTemplateData(map[string]any{"name": "a"}).BodyTemplate("{{ .Extra.nmae }}")))

	status, header, body := getFull(t, m, "/x")
	require.Equal(t, StatusNoMatch, status)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.Contains(t, body, `map has no entry for key "nmae"`)
}

func TestReplyTemplates_ADecodedSegmentStaysInItsDirectory(t *testing.T) {
	// Path segments are decoded, so "..%2Fsecret.txt" is one segment, "../secret.txt": a body file name rendered from
	// a segment must stay in its directory, as one rendered from a query does.
	root := t.TempDir()
	dir := filepath.Join(root, "files")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret.txt"), []byte("the secret"), 0o600))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/files/*").Reply(OK().BodyFile(filepath.Join(dir, "{{ index .Request.URLPathSegments 1 }}"))))

	status, header, body := getFull(t, m, "/files/..%2Fsecret.txt")
	require.Equal(t, StatusNoMatch, status, body)
	require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
	require.NotContains(t, body, "the secret")
}

func TestReplyTemplates_RequestErrors(t *testing.T) {
	// What fails at a request, rendering a template or reading a body file, answers with the error instead of a
	// response with a part missing.
	root := t.TempDir()
	dir := filepath.Join(root, "files")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.txt"), []byte("{{ .X"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret.txt"), []byte("the secret"), 0o600))

	f := `{{ .Request.URL.Query.Get "f" }}`
	noData := make(map[string]any)

	tests := []struct {
		name  string
		reply *Reply
		query string
		want  string
	}{
		{"a header that fails", OK().SetTemplateData(noData).HeaderTemplate("X-A", "{{ .Extra.nmae }}"), "", `template: header.X-A:1:9: executing "header.X-A" at <.Extra.nmae>: map has no entry for key "nmae"`},
		{"a body file name that fails", OK().SetTemplateData(noData).BodyFile("{{ .Extra.nmae }}.txt"), "", `template: body_file:1:9: executing "body_file" at <.Extra.nmae>: map has no entry for key "nmae"`},
		{"a body file name rendered out of its directory", OK().BodyFile(filepath.Join(dir, f)), "?f=../secret.txt", "../secret.txt"},
		{"a body file that is gone", OK().BodyFile(filepath.Join(dir, "gone.txt")), "", "gone.txt"},
		{"a body file name that names a directory", OK().BodyFileWithTemplate(filepath.Join(dir, f)), "?f=sub", "is a directory"},
		{"body file content that doesn't compile", OK().BodyFileWithTemplate(filepath.Join(dir, "broken.txt")), "", "broken.txt:1: unclosed action"},
	}

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	for i, tc := range tests {
		m.MustMock(Getf("/%d", i).Reply(tc.reply))
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, header, body := getFull(t, m, "/"+strconv.Itoa(i)+tc.query)
			require.Equal(t, StatusNoMatch, status, body)
			require.Equal(t, vlatte.FailureReply, header.Get(HeaderVlatteError))
			require.Contains(t, body, tc.want)
			require.NotContains(t, body, "the secret")
		})
	}
}

func TestReply_ProblemAnswersWithProblemJSON(t *testing.T) {
	// Clients pick their decoder by the media type, and RFC 9457 asks the problem's status to match the response's.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		name       string
		reply      *Reply
		wantStatus int
	}{
		{
			name:       "the problem's status sets the response's",
			reply:      OK().Problem(NewProblem().Status(http.StatusConflict)),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "a problem without status keeps the reply's",
			reply:      NotFound().Problem(NewProblem().Title("Not found")),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "a later Status overrides the problem's",
			reply:      NewReply().Problem(NewProblem().Status(http.StatusConflict)).Status(http.StatusBadRequest),
			wantStatus: http.StatusBadRequest,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := "/problem/" + strconv.Itoa(i)
			m.MustMock(Getf("%s", path).Reply(tc.reply))

			res, err := http.Get(m.URL(path))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.wantStatus, res.StatusCode)
			require.Equal(t, MIMEApplicationProblemJSON, res.Header.Get(HeaderContentType))
		})
	}
}
