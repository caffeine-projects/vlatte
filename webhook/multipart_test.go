package webhook_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// complete serves PUT /orders/42 on a server with the webhook, with parameters as its on_complete event.
func complete(t *testing.T, parameters vlatte.Parameters) {
	t.Helper()

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Putf("/orders/42").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: parameters}).
		Reply(mockhttp.NoContent()))

	req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestWebHook_MultipartBodies(t *testing.T) {
	// Webhooks send multipart bodies built the same way as replies: from parameters, from multipart_template, which
	// renders with the request that completed the mock, and from Go. The target matches them with form patterns.
	target := mockhttp.MustNew(mockhttp.Setup().Silent())
	target.MustStart()
	t.Cleanup(target.Close)

	tests := []struct {
		name       string
		targetMock *mockhttp.Builder
		parameters vlatte.Parameters
	}{
		{
			"parameters",
			mockhttp.Postf("/hooks").
				FormField("title", match.Eq("Holiday")).
				FormFile("doc", mockhttp.FileWith().Filename(match.Eq("a.txt")).Body(match.Eq("hello"))).
				Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "multipart": map[string]any{
				"type": "form-data",
				"parts": []any{
					map[string]any{"name": "title", "body": "Holiday"},
					map[string]any{"name": "doc", "filename": "a.txt", "body": "hello"},
				},
			}},
		},
		{
			"multipart_template",
			mockhttp.Postf("/hooks").FormField("order", match.Eq("42")).Reply(mockhttp.OK()),
			map[string]any{"method": "POST", "url": target.URL("/hooks"), "multipart_template": map[string]any{
				"type":  "form-data",
				"parts": []any{map[string]any{"name": "order", "body": "{{ index .Request.URLPathSegments 1 }}"}},
			}},
		},
		{
			"Go",
			mockhttp.Postf("/hooks").
				FormFile("doc", mockhttp.FileWith().ContentType(match.Eq("application/json")).Body(match.JSONPath("$.id", match.Eq(1)))).
				Reply(mockhttp.OK()),
			webhook.Options().Method("POST").URL(target.URL("/hooks")).
				Multipart(mockhttp.MultipartForm(mockhttp.NewPart().Name("doc").Filename("b.json").BodyJSON(map[string]any{"id": 1}))).
				Build("webhook").Parameters,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer target.Clean(t.Context())

			scoped := target.MustMock(tc.targetMock)
			complete(t, tc.parameters)
			scoped.AssertNumberOfCalls(t, 1)
		})
	}
}

func TestWebHook_MultipartErrors(t *testing.T) {
	// Args built in code have no decoder to report conflicts, and templates parse at send time: Run fails, saying
	// why, before sending anything.
	wh, err := webhook.New()
	require.NoError(t, err)

	// Templates parse when the webhook sends, as body_template does.
	brokenTemplate, err := webhook.ArgsFromMap(map[string]any{
		"url":                "http://localhost",
		"multipart_template": map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "{{ .X"}}},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		args *webhook.Args
		want string
	}{
		{"two bodies", &webhook.Args{URL: "http://localhost", Body: "x", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart())}, "webhook: bad argument: more than one body is set: keep one of Body, BodyTemplate, BodyFile and Multipart"},
		{"a template that doesn't parse", brokenTemplate, "webhook: multipart body: multipart: parts[0]: body: template: body:1: unclosed action"},
		{"a Content-Type header", &webhook.Args{URL: "http://localhost", Header: http.Header{"content-type": {"x"}}, Multipart: mockhttp.MultipartMixed(mockhttp.NewPart())}, "webhook: bad argument: a multipart body sets the Content-Type: remove the Content-Type header"},
		{"a body that can't be written", &webhook.Args{URL: "http://localhost", Multipart: mockhttp.MultipartForm(mockhttp.NewPart())}, "webhook: multipart body: multipart: parts[0].name is required in a form-data part; name the form field"},
		{"a Go part with a delay", &webhook.Args{URL: "http://localhost", Multipart: mockhttp.MultipartMixed(mockhttp.NewPart().Delay(time.Second))}, "webhook: multipart body: multipart: parts[0].delay can't be set: a body written at once doesn't pause; delays apply to replies only"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.EqualError(t, wh.Run(t.Context(), onComplete{params: tc.args}), tc.want)
		})
	}
}

func TestWebHook_RelativeFilesOpenFromTheMockFile(t *testing.T) {
	// A mock file's webhooks open relative files from the mock file's directory, as its replies do, wherever the
	// server runs: a whole body_file and a multipart part's alike. A document given as bytes has no directory, and
	// opens them from the working directory, as global events do, whatever mock they complete.
	got := make(chan string, 3)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + " " + string(b)
	}))
	t.Cleanup(target.Close)

	wh, err := webhook.New()
	require.NoError(t, err)

	global := vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{"method": "POST", "url": target.URL + "/global", "body_file": "body.txt"}}

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh).
		GlobalEvents(&vlatte.Events{OnComplete: vlatte.EventConfigs{global}}))
	m.MustStart()
	t.Cleanup(m.Close)

	doc := fmt.Sprintf(`
request: {method: PUT, path: /orders/42}
response: {status: 204}
on_complete:
  - {name: webhook, parameters: {method: POST, url: "%[1]s/whole", body_file: body.txt}}
  - {name: webhook, parameters: {method: POST, url: "%[1]s/part", multipart: {type: mixed, boundary: b, parts: [{body_file: part.txt}]}}}
`, target.URL)

	mocks := t.TempDir()
	write(t, filepath.Join(mocks, "body.txt"), "body")
	write(t, filepath.Join(mocks, "part.txt"), "part")
	write(t, filepath.Join(mocks, "hook.mock.yaml"), doc)

	t.Chdir(t.TempDir())
	write(t, "body.txt", "cwd body")
	write(t, "part.txt", "cwd part")

	run := func(b vlatte.Builder[*mockhttp.Mock, *mockhttp.Server]) []string {
		scope := m.MustMock(b)
		defer scope.Clear(context.Background())

		req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		sent := []string{<-got, <-got, <-got}
		slices.Sort(sent)

		return sent
	}

	require.Equal(t, []string{"/global cwd body", "/part --b\r\n\r\npart\r\n--b--\r\n", "/whole body"},
		run(mockhttp.FromFile(filepath.Join(mocks, "hook.mock.yaml"))), "from the mock file's directory")
	require.Equal(t, []string{"/global cwd body", "/part --b\r\n\r\ncwd part\r\n--b--\r\n", "/whole cwd body"},
		run(mockhttp.FromBytes([]byte(doc), "yaml")), "from the working directory")
}

func write(t *testing.T, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
}

// received is a multipart form as a webhook target parsed it.
type received struct {
	values map[string][]string
	files  map[string][]string // filename=content
}

func TestPropWebHook_MultipartReachesTheTarget(t *testing.T) {
	// W2: a multipart body, from parameters or from Go, reaches the target as the modeled form: its fields, and its
	// files with their names and content. Templated text renders with the request that completed the mock.
	got := make(chan received, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			got <- received{}

			return
		}

		files := make(map[string][]string)
		for name, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				f, _ := fh.Open()
				b, _ := io.ReadAll(f)
				f.Close()

				files[name] = append(files[name], fh.Filename+"="+string(b))
			}
		}

		got <- received{values: r.MultipartForm.Value, files: files}
	}))
	t.Cleanup(target.Close)

	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().OnCompleteListener("webhook", wh))
	m.MustStart()
	t.Cleanup(m.Close)

	token := rapid.StringMatching(`[A-Za-z0-9_.-]{1,8}`)

	rapid.Check(t, func(t *rapid.T) {
		templated := rapid.Bool().Draw(t, "templated")
		fromGo := !templated && rapid.Bool().Draw(t, "from Go")

		n := rapid.IntRange(1, 4).Draw(t, "parts")
		parts := make([]any, 0, n)
		goParts := make([]*mockhttp.Part, 0, n)
		want := received{values: make(map[string][]string), files: make(map[string][]string)}

		rendered := func(s string) string { return s }
		if templated {
			rendered = func(s string) string { return s + "PUT" }
		}

		for range n {
			name := token.Draw(t, "name")
			body := token.Draw(t, "body")

			text := body
			if templated {
				text += "{{ .Request.Method }}"
			}

			part := map[string]any{"name": name, "body": text}
			goPart := mockhttp.NewPart().Name(name).BodyText(text)

			if rapid.Bool().Draw(t, "file") {
				filename := token.Draw(t, "filename")
				part["filename"] = filename
				goPart.Filename(filename)
				want.files[name] = append(want.files[name], filename+"="+rendered(body))
			} else {
				want.values[name] = append(want.values[name], rendered(body))
			}

			parts = append(parts, part)
			goParts = append(goParts, goPart)
		}

		var parameters vlatte.Parameters

		switch {
		case fromGo:
			parameters = &webhook.Args{Method: "POST", URL: target.URL, Multipart: mockhttp.MultipartForm(goParts...)}
		case templated:
			parameters = map[string]any{"method": "POST", "url": target.URL, "multipart_template": map[string]any{"type": "form-data", "parts": parts}}
		default:
			parameters = map[string]any{"method": "POST", "url": target.URL, "multipart": map[string]any{"type": "form-data", "parts": parts}}
		}

		scope, err := m.Mock(mockhttp.Putf("/orders/42").
			OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: parameters}).
			Reply(mockhttp.NoContent()))
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		req, err := http.NewRequest(http.MethodPut, m.URL("/orders/42"), nil)
		require.NoError(t, err)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		select {
		case r := <-got:
			require.Equal(t, want.values, r.values)
			require.Equal(t, want.files, r.files)
		case <-time.After(5 * time.Second):
			t.Fatalf("the webhook didn't reach the target")
		}
	})
}

// webhookFault is an invalid setting injected into valid multipart parameters, and the problem it must cause.
type webhookFault struct {
	area   string
	code   string
	key    string
	inject func(params map[string]any)
}

// _webhookFaults touch distinct settings of valid multipart parameters.
var _webhookFaults = []webhookFault{
	{"body", config.CodeConflict, "parameters.multipart", func(p map[string]any) { p["body"] = "x" }},
	{"template", config.CodeConflict, "parameters.multipart_template", func(p map[string]any) {
		// An object of its own: the faults of multipart mustn't show up twice.
		p["multipart_template"] = map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "z"}}}
	}},
	{"header", config.CodeConflict, "parameters.header.Content-Type", func(p map[string]any) { p["header"] = map[string]any{"Content-Type": "x"} }},
	{"delay", config.CodeNotAllowed, "parameters.multipart.parts[0].delay", func(p map[string]any) {
		multipartOf(p)["parts"].([]any)[0].(map[string]any)["delay"] = "1s"
	}},
	{"boundary", config.CodeInvalidValue, "parameters.multipart.boundary", func(p map[string]any) { multipartOf(p)["boundary"] = "a b " }},
	{"type", config.CodeMissingValue, "parameters.multipart.parts[1].name", func(p map[string]any) { multipartOf(p)["type"] = "form-data" }},
}

func multipartOf(params map[string]any) map[string]any {
	m, _ := params["multipart"].(map[string]any)
	return m
}

func TestPropArgsFromMap_EveryMultipartFaultOnce(t *testing.T) {
	// W3: each fault of multipart parameters is reported once, with its code and key, and nothing else is: one pass
	// fixes them all.
	rapid.Check(t, func(t *rapid.T) {
		params := map[string]any{
			"url": "http://localhost",
			"multipart": map[string]any{
				"type": "mixed",
				"parts": []any{
					map[string]any{"name": "a", "body": "x"},
					map[string]any{"body": "y"},
				},
			},
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_webhookFaults), 1, len(_webhookFaults),
			func(f webhookFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(params)
			want = append(want, f.code+" "+f.key)
		}

		_, err := webhook.ArgsFromMap(params)
		require.ErrorIs(t, err, webhook.ErrBadArg)

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok)

		got := make([]string, 0, len(cfgErr.Problems))
		for _, p := range cfgErr.Problems {
			got = append(got, p.Code+" "+p.Key)
		}

		slices.Sort(got)
		slices.Sort(want)
		require.Equal(t, want, got)
	})
}
