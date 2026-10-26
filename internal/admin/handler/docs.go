package handler

import (
	_ "embed"
	"net/http"
	"strings"
)

const (
	// _scalarVersion is the version of Scalar the docs page loads unless it is given another.
	_scalarVersion = "1.75.1"

	// _scalarIntegrity is the hash of the file npm publishes for _scalarVersion.
	_scalarIntegrity = "sha384-G+2Q2i9l3PPfmYkk0b4BAIUrs7xb/DNaTL1wCYYab4c9dIKlCyiqzC0Qx3eq9Wf6"
)

// _docsPage renders the OpenAPI document with Scalar. Its configuration and styles are the place to change how the
// documentation looks. Docs fills in {version} and {integrity}.
//
//go:embed docs.html
var _docsPage string

// Docs answers the page that documents the admin API from its OpenAPI document: GET / and GET /docs. The page loads
// Scalar at version, or at the version vlatte pins when it is empty. Only the pinned version is checked against the
// hash of its file: the hash of another isn't known. version goes into the page as it is: the caller checks it is a
// version.
func Docs(version string) http.Handler {
	integrity := `integrity="` + _scalarIntegrity + `"`
	if version == "" || version == _scalarVersion {
		version = _scalarVersion
	} else {
		integrity = ""
	}

	return page([]byte(strings.NewReplacer("{version}", version, "{integrity}", integrity).Replace(_docsPage)))
}

// page answers an HTML page.
func page(html []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	})
}
