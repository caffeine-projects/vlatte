package handler

import (
	_ "embed"
	"html"
	"net/http"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/buildinfo"
)

// _homePage shows what the mock server serves and what it served, from GET /health, /readyz, /mocks, /requests,
// /scenarios and /recording, and links to the other pages. Home fills in {vlatte-version}.
//
//go:embed home.html
var _homePage string

// Home answers the homepage of the admin API: GET /. It shows the state of the mock server, and boxes for what the
// server can't do say so.
func Home() http.Handler {
	return page(strings.ReplaceAll(layout(_homePage, "home"), "{vlatte-version}", html.EscapeString(buildinfo.Version())))
}
