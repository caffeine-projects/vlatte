package mockgrpc

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// methodName matches a full method, such as "/pkg.Service/Method", by a name of whole segments: its end after a "/"
// or a ".", such as "Method", "/Method", "Service/Method", "pkg.Service/Method" or the full method. "Meth" doesn't
// match.
func methodName(name string) vlatte.Matcher {
	name = strings.TrimPrefix(name, "/")

	return match.Any(match.HasSuffix("/"+name), match.HasSuffix("."+name))
}
