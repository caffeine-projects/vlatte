package mockhttp

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

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
