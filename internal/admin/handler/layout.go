package handler

import (
	_ "embed"
	"strings"
)

const (
	// _layoutHeadSlot is where a page takes the shared head: its colors and the navbar's styles.
	_layoutHeadSlot = "<!-- layout:head -->"

	// _layoutNavSlot is where a page takes the navbar.
	_layoutNavSlot = "<!-- layout:nav -->"
)

var (
	// _layoutHead holds the colors of the light and dark themes, the visitor's pick of one, and the navbar's styles.
	//
	//go:embed layout_head.html
	_layoutHead string

	// _layoutNav is the navbar: a link to each admin page, a link to the repository, and the theme toggle.
	//
	//go:embed layout_nav.html
	_layoutNav string
)

// layout fills the slots of an admin page with the layout every page shares, and marks the navbar's link to current,
// the data-page of the page's link, as the page shown.
func layout(html, current string) string {
	mark := `data-page="` + current + `"`
	nav := strings.Replace(_layoutNav, mark, mark+` aria-current="page"`, 1)

	return strings.NewReplacer(_layoutHeadSlot, _layoutHead, _layoutNavSlot, nav).Replace(html)
}
