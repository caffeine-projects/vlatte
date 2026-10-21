package handler

import (
	_ "embed"
	"net/http"
)

// _docsPage renders the OpenAPI document with Scalar. Its configuration and styles are the place to change how the
// documentation looks.
//
//go:embed docs.html
var _docsPage []byte

// Docs answers the page that documents the admin API from its OpenAPI document: GET /docs.
func Docs() http.Handler {
	return page(_docsPage)
}

// page answers an HTML page.
func page(html []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	})
}
