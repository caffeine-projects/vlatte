package mockhttp

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
)

// genKey draws a key as users write them: mixed case, dots, spaces, YAML traps and any unicode.
func genKey() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{"userId", "UserID", "user_id", "a.b", "a b", "<<", "1", "true", "~", "Content-Type"}),
		rapid.String(),
	)
}

func TestPropMockFile_KeysKeepTheirText(t *testing.T) {
	// M8 (B6): keys reach templates, ext handlers and listeners exactly as written.
	handler := &capturingHandler{}
	params := make(chan any, 1)
	capture := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, args vlatte.OnComplete) error {
		params <- args.Parameters()
		return nil
	})

	m := MustNew(Setup().Silent().MockFileHandlers(handler).OnCompleteListener("capture", capture))
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		key := genKey().Draw(t, "key")
		value := genToken().Draw(t, "value")
		reqPath := "/keys/" + genToken().Draw(t, "path")

		doc := map[string]any{
			"request": map[string]any{"path": reqPath},
			"response": map[string]any{
				"body":     "{{ index .Extra " + strconv.Quote(key) + " }}",
				"template": map[string]any{"data": map[string]any{key: value}},
			},
			"ext":         map[string]any{key: value},
			"on_complete": []any{map[string]any{"name": "capture", "parameters": map[string]any{key: value}}},
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}})
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		require.Equal(t, map[string]any{key: value}, handler.got, "ext")

		res, body := send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, value, string(body), "template.data")

		select {
		case got := <-params:
			require.Equal(t, map[string]any{key: value}, got, "on_complete parameters")
		case <-time.After(5 * time.Second):
			t.Fatalf("the listener didn't run")
		}
	})
}

func TestPropMockFile_BodyFileIsRelativeToTheMockFile(t *testing.T) {
	// M10 (F17): a relative body_file opens from the mock file's directory, wherever the server runs;
	// a document without a file opens it from the working directory.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	files := t.TempDir()
	t.Chdir(t.TempDir())

	wd, err := os.Getwd()
	require.NoError(t, err)

	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(files, "mocks")
		require.NoError(t, err)

		segments := rapid.SliceOfN(rapid.StringMatching(`[a-z0-9_-]{1,6}`), 1, 3).Draw(t, "body file")
		rel := path.Join(segments...) + ".txt"
		content := genToken().Draw(t, "content")
		reqPath := "/files/" + genToken().Draw(t, "path")

		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		doc := map[string]any{"request": map[string]any{"path": reqPath}, "response": map[string]any{"body_file": rel}}
		mockFile := filepath.Join(dir, "body.mock."+format)
		writeFile(t, mockFile, string(encodeMock(t, doc, format)))

		scope, err := m.Mock(FromFile(mockFile))
		require.NoError(t, err)

		_, body := send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, content, string(body), "from the mock file's directory")
		require.NoError(t, scope.Clear(context.Background()))

		writeFile(t, filepath.Join(wd, filepath.FromSlash(rel)), content+"~")

		scope, err = m.Mock(FromBytes(encodeMock(t, doc, format), format))
		require.NoError(t, err)

		_, body = send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, content+"~", string(body), "from the working directory")
		require.NoError(t, scope.Clear(context.Background()))
	})
}

func writeFile(t require.TestingT, name, content string) {
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o700))
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
}

func TestPropAdmin_AddsMocksByID(t *testing.T) {
	// M15: a mock posted to the admin API is stored under its id and served; posting the id again is a conflict.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	seq := 0

	rapid.Check(t, func(t *rapid.T) {
		model := genMock(m.URL()).Draw(t, "mock")
		model.enabled, model.repeat = true, 0

		seq++
		model.id = "admin-" + strconv.Itoa(seq)

		body, err := json.Marshal(model.doc(), json.Deterministic(true))
		require.NoError(t, err)

		post := func() int {
			res, err := http.Post(m.URL(m.Config().AdminPath, "mocks"), MIMEApplicationJSON, strings.NewReader(string(body)))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			if res.StatusCode == http.StatusCreated {
				require.Equal(t, "/mocks/"+model.id, res.Header.Get(HeaderLocation))
			}

			return res.StatusCode
		}

		require.Equal(t, http.StatusCreated, post())

		defer m.storage.Delete(context.Background(), model.id)

		res, err := http.Get(m.URL(m.Config().AdminPath, "mocks", model.id))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		req := model.request.matching(t)
		res, got := send(t, m.URL(), req)

		served := model.served()[0]
		if model.random != nil {
			served = model.random[0]
			for _, r := range model.random {
				if replyDiff(r, req.method, res, got) == "" {
					served = r
				}
			}
		}

		require.Empty(t, replyDiff(served, req.method, res, got))
		require.Equal(t, http.StatusConflict, post(), "the id is taken")
	})
}
