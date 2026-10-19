package mockhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkMultipartForm(b *testing.B) {
	// A 1 MB upload through the parser chain: net/http parses it in memory, a copy of the body at most.
	files := make([]formFile, 50)
	for i := range files {
		files[i] = formFile{field: "f", filename: "f.bin", body: bytes.Repeat([]byte{byte(i)}, 20<<10)}
	}

	contentType, body := formModel{fields: []formField{{name: "a", value: "1"}}, files: files}.encode(b)
	app := MustNew(Setup().Silent())

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()

	for b.Loop() {
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		r.Header.Set(HeaderContentType, contentType)

		if _, _, _, err := parseRequestBody(app, r); err != nil {
			b.Fatal(err)
		}
	}
}
