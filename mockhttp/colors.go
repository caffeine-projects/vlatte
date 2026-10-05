package mockhttp

import "os"

// ansi styles text with ANSI escape codes when true.
// Each style closes with its own code, so styles nest.
type ansi bool

func (a ansi) wrap(on, off, s string) string {
	if !a {
		return s
	}

	return "\x1b[" + on + "m" + s + "\x1b[" + off + "m"
}

func (a ansi) Bold(s string) string         { return a.wrap("1", "22", s) }
func (a ansi) Red(s string) string          { return a.wrap("31", "39", s) }
func (a ansi) Green(s string) string        { return a.wrap("32", "39", s) }
func (a ansi) Yellow(s string) string       { return a.wrap("33", "39", s) }
func (a ansi) Blue(s string) string         { return a.wrap("34", "39", s) }
func (a ansi) BrightRed(s string) string    { return a.wrap("91", "39", s) }
func (a ansi) BrightGreen(s string) string  { return a.wrap("92", "39", s) }
func (a ansi) BrightYellow(s string) string { return a.wrap("93", "39", s) }
func (a ansi) BrightBlue(s string) string   { return a.wrap("94", "39", s) }

// colorsEnabled reports whether to style output: colors are configured, the output is a terminal,
// and neither NO_COLOR nor TERM=dumb opts out.
func colorsEnabled(configured, terminal bool) bool {
	return configured && terminal && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}
