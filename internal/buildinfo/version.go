// Package buildinfo reports which vlatte a program was built with.
package buildinfo

import (
	"runtime/debug"
	"slices"
)

// _modulePath is the path of the vlatte module.
const _modulePath = "github.com/caffeine-projects/vlatte"

// Version is the version of vlatte the program was built with, as its main module or as a dependency: the tag it was
// built at, or a pseudo-version naming its commit, with +dirty for uncommitted changes. It is "devel" when the build
// carries no version, such as one with -buildvcs=false.
func Version() string {
	info, _ := debug.ReadBuildInfo()

	return versionFrom(info)
}

func versionFrom(info *debug.BuildInfo) string {
	if info == nil {
		return "devel"
	}

	modules := append([]*debug.Module{&info.Main}, info.Deps...)
	if i := slices.IndexFunc(modules, func(m *debug.Module) bool { return m.Path == _modulePath }); i >= 0 {
		if v := modules[i].Version; v != "" && v != "(devel)" {
			return v
		}
	}

	return "devel"
}
