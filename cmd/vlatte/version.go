package main

import "runtime/debug"

// version reports the module version of binaries installed with go install …@version,
// or the VCS revision of binaries built from a checkout.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}

	return versionFrom(info)
}

func versionFrom(info *debug.BuildInfo) string {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}

	var revision string
	var modified bool

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}

	if revision == "" {
		return "devel"
	}

	if len(revision) > 12 {
		revision = revision[:12]
	}

	if modified {
		revision += "+dirty"
	}

	return revision
}
