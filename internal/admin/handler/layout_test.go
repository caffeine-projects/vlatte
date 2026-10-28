package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLayout_EveryPageHasTheNavbarAndMarksItsOwnLink(t *testing.T) {
	// Every admin page shares one navbar, under the vlatte mark: a relative link to each page, so it works wherever the
	// admin API is mounted, with the page shown marked, then the repository link and the theme toggle, in that order.
	base := serve(t, newStore(t))

	tests := []struct {
		path string
		page string
	}{
		{path: "/", page: "home"},
		{path: "/scenario-board", page: "scenario-board"},
		{path: "/recorder", page: "recorder"},
		{path: "/docs", page: "docs"},
	}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			_, body := send(t, request{method: http.MethodGet, url: base + tt.path})

			require.Equal(t, 1, strings.Count(body, `<nav class="vl-nav"`))
			require.Contains(t, body, `<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,`, "the tab shows the vlatte mark")
			require.Contains(t, body, `<span class="vl-wordmark"><span>v</span>latte</span>`)
			require.NotContains(t, body, "<!-- layout:")
			for _, link := range []string{
				`<a href="./" data-page="home"`,
				`<a href="scenario-board" data-page="scenario-board"`,
				`<a href="recorder" data-page="recorder"`,
				`<a href="docs" data-page="docs"`,
			} {
				require.Contains(t, body, link)
			}

			require.Equal(t, 1, strings.Count(body, `" aria-current="page"`), "one link is marked")
			require.Contains(t, body, `data-page="`+tt.page+`" aria-current="page"`)

			github := `href="https://github.com/caffeine-projects/vlatte"`
			toggle := `id="vl-theme"`
			require.Contains(t, body, github)
			require.Contains(t, body, toggle)
			require.Less(t, strings.Index(body, github), strings.Index(body, toggle), "the theme toggle comes after the repository link")
		})
	}
}

func TestLayout_AppliesTheThemeTheVisitorPickedBeforeThePagePaints(t *testing.T) {
	// The pick of the toggle holds on every page: the head reads it before the body, so a page doesn't flash the
	// theme of the system first.
	base := serve(t, newStore(t))

	for _, path := range []string{"/", "/scenario-board", "/recorder", "/docs"} {
		_, body := send(t, request{method: http.MethodGet, url: base + path})

		pick := "localStorage.getItem('vlatte-theme')"
		require.Contains(t, body, pick, path)
		require.Less(t, strings.Index(body, pick), strings.Index(body, "<body>"), path)
		require.Contains(t, body, `:root[data-theme="dark"]`, path)
	}
}
