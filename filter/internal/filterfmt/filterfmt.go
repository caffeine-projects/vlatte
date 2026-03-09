package filterfmt

import "regexp"

var _indentRegExp = regexp.MustCompile("(?m)^")

func Indent(str string) string {
	return _indentRegExp.ReplaceAllString(str, " ")
}
