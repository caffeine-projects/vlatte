package mockgrpc

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

// namesMethod reports whether name names the full method full, such as "/pkg.Service/Method", by whole segments: its
// end after a "/" or a ".", such as "Method", "/Method", "Service/Method", "pkg.Service/Method" or the full method.
// "Meth" doesn't name it.
func namesMethod(name, full string) bool {
	name = strings.TrimPrefix(name, "/")

	return strings.HasSuffix(full, "/"+name) || strings.HasSuffix(full, "."+name)
}

// methodIn matches a full method that is one of methods, exactly: the methods a mock's name resolved to.
func methodIn(methods []string) vlatte.Matcher {
	return match.IsIn(methods)
}
