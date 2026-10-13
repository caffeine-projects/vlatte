package e2e

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// checkTemplateSites serves tmpl at every place a template can be, on a server that compiles templates with engine:
// a reply's body, header, body file name, body file content and multipart part, and a webhook's url, header, body
// and multipart part. Each place is served by a mock built in Go and by a mock file, and must render want for the
// request PUT /<mock>/42. tmpl is in the engine's syntax.
func checkTemplateSites(t *testing.T, engine vlatte.TemplateEngine, tmpl, want string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, want+".txt"), []byte("named "+want), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "content.txt"), []byte(tmpl), 0o600))

	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)

	hook := target.MustMock(mockhttp.Postf("/hooks/%s", want).Header("X-Out", match.Eq(want)).Body(match.Eq(want)).Reply(mockhttp.OK()))
	hookPart := target.MustMock(mockhttp.Postf("/parts").FormField("out", match.Eq(want)).Reply(mockhttp.OK()))

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(engine).OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	hooks := []map[string]any{
		{
			"name": "webhook",
			"parameters": map[string]any{
				"method": "POST", "url_template": target.URL() + "/hooks/" + tmpl,
				"header_template": map[string]any{"X-Out": tmpl}, "body_template": tmpl,
			},
		},
		{
			"name": "webhook",
			"parameters": map[string]any{
				"method": "POST", "url": target.URL("/parts"),
				"multipart_template": map[string]any{"type": "form-data", "parts": []any{map[string]any{"name": "out", "body": tmpl}}},
			},
		},
	}

	// Each place, as a reply built in Go and as the response of a mock file.
	sites := []struct {
		name  string
		reply *mockhttp.Reply
		doc   map[string]any
	}{
		{"body", mockhttp.OK().BodyTemplate(tmpl), map[string]any{"body": tmpl, "template": map[string]any{"enabled": true}}},
		{"header", mockhttp.OK().HeaderTemplate("X-Out", tmpl), map[string]any{"header_template": map[string]any{"X-Out": tmpl}}},
		{"name", mockhttp.OK().BodyFile(filepath.Join(dir, tmpl+".txt")), map[string]any{"body_file": filepath.Join(dir, tmpl+".txt")}},
		{
			"content", mockhttp.OK().BodyFileWithTemplate(filepath.Join(dir, "content.txt")),
			map[string]any{"body_file": filepath.Join(dir, "content.txt"), "template": map[string]any{"enabled": true}},
		},
		{
			"part", mockhttp.OK().Multipart(mockhttp.MultipartMixed(mockhttp.NewPart().BodyText(tmpl)).Boundary("b").Template()),
			map[string]any{"multipart": map[string]any{"type": "mixed", "boundary": "b", "parts": []any{map[string]any{"body": tmpl}}}, "template": map[string]any{"enabled": true}},
		},
	}

	for _, site := range sites {
		goMock := mockhttp.Putf("/go-%s/42", site.name).Reply(site.reply)
		doc := map[string]any{"request": map[string]any{"method": "PUT", "path": "/file-" + site.name + "/42"}, "response": site.doc}

		// The webhooks go with the body, which is served twice: once from Go and once from the mock file.
		if site.name == "body" {
			for _, h := range hooks {
				goMock.OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: h["parameters"]})
			}

			doc["on_complete"] = []any{hooks[0], hooks[1]}
		}

		data, err := json.Marshal(doc)
		require.NoError(t, err)

		m.MustMock(goMock, mockhttp.FromBytes(data, "json"))
	}

	for _, source := range []string{"go", "file"} {
		for _, site := range sites {
			path := "/" + source + "-" + site.name + "/42"

			req, err := http.NewRequest(http.MethodPut, m.URL()+path, nil)
			require.NoError(t, err)

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			body := string(b)
			require.Equal(t, http.StatusOK, res.StatusCode, "%s: %s", path, body)

			switch site.name {
			case "header":
				require.Equal(t, want, res.Header.Get("X-Out"), path)
			case "name":
				require.Equal(t, "named "+want, body, path)
			case "part":
				require.True(t, strings.Contains(body, "\r\n\r\n"+want+"\r\n"), "%s: %q", path, body)
			default:
				require.Equal(t, want, body, path)
			}
		}
	}

	// Each webhook ran for both bodies, after their responses.
	m.WaitEvents()
	require.True(t, hook.AssertNumberOfCalls(t, 2))
	require.True(t, hookPart.AssertNumberOfCalls(t, 2))
}
