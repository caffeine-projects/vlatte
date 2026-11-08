// Package pathtemplate reads the paths of an OpenAPI document, whose segments may hold parameters, such as {id}. The
// mock generator and the openapi matcher read them alike: the matcher routes a request to the operation whose mocks
// the generator orders first.
package pathtemplate

import (
	"regexp"
	"strings"
)

// Param matches a parameter of a path, such as {id}.
var Param = regexp.MustCompile(`\{[^{}/]+\}`)

// Templated returns the number of segments of path that hold a parameter. Of two paths that match a request, OpenAPI
// takes the one with fewer, as /pets/mine before /pets/{id}.
func Templated(path string) int {
	n := 0
	for seg := range strings.SplitSeq(path, "/") {
		if Param.MatchString(seg) {
			n++
		}
	}

	return n
}
