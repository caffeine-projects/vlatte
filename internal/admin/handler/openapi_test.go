package handler_test

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestOpenAPI(t *testing.T) {
	// The document is served in both formats, which say the same.
	url := serve(t, newStore(t))

	res, asJSON := send(t, request{method: http.MethodGet, url: url + "/openapi"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))

	res, asYAML := send(t, request{method: http.MethodGet, url: url + "/openapi", accept: "application/yaml"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "application/yaml", res.Header.Get("Content-Type"))

	fromJSON, err := config.Parse([]byte(asJSON), "json")
	require.NoError(t, err)

	fromYAML, err := config.Parse([]byte(asYAML), "yaml")
	require.NoError(t, err)

	require.Equal(t, fromYAML, fromJSON)
	require.Equal(t, "3.2.0", fromJSON.(map[string]any)["openapi"], "3.2 documents the QUERY method of /requests")
}

func TestDocs(t *testing.T) {
	// The documentation page renders the admin API's own document, at the root of the admin API and at /docs, with a
	// renderer pinned to a version and its hash, so a changed file on the CDN doesn't run.
	url := serve(t, newStore(t))

	for _, path := range []string{"/", "/docs"} {
		res, body := send(t, request{method: http.MethodGet, url: url + path})

		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.Equal(t, "text/html; charset=utf-8", res.Header.Get("Content-Type"), path)
		require.Contains(t, body, `url: 'openapi'`, path)
		require.Regexp(t, `src="https://cdn\.jsdelivr\.net/npm/@scalar/api-reference@\d+\.\d+\.\d+/dist/browser/standalone\.js"`, body, path)
		require.Regexp(t, `integrity="sha384-[A-Za-z0-9+/=]+"`, body, path)
	}
}

func TestDocs_LoadsTheVersionItIsGiven(t *testing.T) {
	// A newer renderer than the pinned one loads without a hash, which only the pinned version has: with the pinned
	// one's, the browser would refuse it.
	s := start(t, admin.Options{DocsVersion: "1.80.0"}, newStore(t), &fakeBuilder{}, adminapi.Capabilities{})

	res, body := send(t, request{method: http.MethodGet, url: s.URL() + "/docs"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, body, `src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.80.0/dist/browser/standalone.js"`)
	require.NotContains(t, body, "integrity=")
}

func TestOpenAPI_KeepsRelatedOperationsTogether(t *testing.T) {
	// Renderers list operations in the order of the document: in JSON too, the operations of each tag come together,
	// in the order of the tags.
	url := serve(t, newStore(t))

	_, body := send(t, request{method: http.MethodGet, url: url + "/openapi"})

	var doc struct {
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Paths map[string]jsontext.Value `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &doc))

	var raw struct {
		Paths jsontext.Value `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &raw))

	var runs []string
	for _, path := range keysInOrder(t, raw.Paths) {
		item := doc.Paths[path]

		var operations map[string]jsontext.Value
		require.NoError(t, json.Unmarshal(item, &operations))

		for _, method := range keysInOrder(t, item) {
			if method == "parameters" {
				continue
			}

			var op struct {
				Tags []string `json:"tags"`
			}
			require.NoError(t, json.Unmarshal(operations[method], &op))
			require.Len(t, op.Tags, 1, method+" "+path)

			if len(runs) == 0 || runs[len(runs)-1] != op.Tags[0] {
				runs = append(runs, op.Tags[0])
			}
		}
	}

	tags := make([]string, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		tags = append(tags, tag.Name)
	}

	require.Equal(t, tags, runs)
}

// keysInOrder returns the keys of the JSON object obj, in the order it has them.
func keysInOrder(t *testing.T, obj jsontext.Value) []string {
	t.Helper()

	dec := jsontext.NewDecoder(bytes.NewReader(obj))

	_, err := dec.ReadToken()
	require.NoError(t, err)

	var keys []string
	for dec.PeekKind() != '}' {
		key, err := dec.ReadToken()
		require.NoError(t, err)

		keys = append(keys, key.String())
		require.NoError(t, dec.SkipValue())
	}

	return keys
}

func TestOpenAPI_DocumentsTheEvents(t *testing.T) {
	// GET /events streams the events of the mock server: the document says what a client of it reads.
	data, err := json.Marshal(adminapi.Event{
		Type: adminapi.EventMatched, Time: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), RequestID: "1", Method: "GET",
		Target: "/hello", MockID: "hello", Status: 200, Message: "matched",
	})
	require.NoError(t, err)

	conformsAs(t, string(data), "json", "Event")
}
