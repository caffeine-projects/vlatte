package e2e

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/openapiv3"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestOpenAPI_TheGeneratedMocksServeTheSpec(t *testing.T) {
	// What the generator writes is only worth what the server makes of it: the files must load, and answer each
	// request as the spec says, the example id with the pet, another id with a 404, a client that takes no offered
	// media type with a 406, and a concrete path before the templated one.
	dir := t.TempDir()

	loc, err := openapi.Location("testdata/openapi/pets.openapi.yaml")
	require.NoError(t, err)

	read := func(u *url.URL) ([]byte, error) { return openapi.Read(t.Context(), http.DefaultClient, u) }

	data, err := read(loc)
	require.NoError(t, err)

	doc, err := openapiv3.Load(t.Context(), data, loc, read)
	require.NoError(t, err)

	out := openapi.Generate(doc)
	require.Empty(t, out.Warnings)

	_, err = openapi.Write(dir, doc.Title, out.Files)
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().Admin(admin.MustNew()).MockFilePatterns(filepath.Join(dir, "*.mock.yaml")))
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		name        string
		path        string
		accept      string
		status      int
		mock        string
		contentType string
	}{
		{name: "the example id", path: "/v1/pets/11111111-1111-4111-8111-111111111111", status: 200, mock: "v1.pets._id.get.200.json.example", contentType: "application/json"},
		{name: "the example id in XML", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "application/xml", status: 200, mock: "v1.pets._id.get.200.xml.example", contentType: "application/xml"},
		{name: "another id", path: "/v1/pets/33333333-3333-4333-8333-333333333333", status: 404, mock: "v1.pets._id.get.404.json", contentType: "application/json"},
		{name: "a media type not offered", path: "/v1/pets/11111111-1111-4111-8111-111111111111", accept: "text/csv", status: 406, mock: "v1.pets._id.get.406", contentType: "text/plain; charset=utf-8"},
		{name: "a concrete path", path: "/v1/pets/mine", status: 200, mock: "v1.pets.mine.get.200.json", contentType: "application/json"},
		{name: "an example that is escaped in a path", path: "/v1/tags/red%20fox", status: 200, mock: "v1.tags._tag.get.200.text.example", contentType: "text/plain"},
		{name: "another value of an escaped example", path: "/v1/tags/red", status: 404, mock: "v1.tags._tag.get.404"},
		{name: "a parameter inside a segment", path: "/v1/files/a.txt", status: 200, mock: "v1.files._name._ext.get.200.text", contentType: "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+tt.path, nil)
			require.NoError(t, err)

			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}

			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)

			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			require.Equal(t, tt.status, res.StatusCode, string(body))
			require.Equal(t, tt.mock, res.Header.Get(vlatte.HeaderMatchedMockID))
			require.Equal(t, tt.contentType, res.Header.Get("Content-Type"))
		})
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+"/v1/pets/mine", nil)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	var pet map[string]any
	require.NoError(t, json.UnmarshalRead(res.Body, &pet))
	require.NoError(t, res.Body.Close())
	require.Equal(t, map[string]any{"id": "22222222-2222-4222-8222-222222222222", "name": "Mine"}, pet)

	// A disabled mock is there to be enabled: once it is, it answers before the mocks the spec predicts.
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPatch, m.AdminURL()+"/mocks/v1.pets._id.get.503", strings.NewReader(`{"enabled": true}`))
	require.NoError(t, err)

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL() + "/v1/pets/11111111-1111-4111-8111-111111111111")
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, "v1.pets._id.get.503", res.Header.Get(vlatte.HeaderMatchedMockID))
}
