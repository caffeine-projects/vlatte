package config

import (
	"maps"
	"os"
	"slices"
	"strings"
)

type envSource struct {
	prefix   string
	reserved []string
}

// Env reads settings from environment variables named prefix_KEY, such as VLATTE_LOG_LEVEL for log.level.
// Empty variables are ignored.
// Any other variable starting with prefix_ is a problem, so a typo doesn't go unnoticed,
// unless it is listed in reserved because something else reads it.
func Env(prefix string, reserved ...string) Source {
	return &envSource{prefix: prefix, reserved: reserved}
}

func (s *envSource) load(c *catalog, _ lookup) ([]entry, []Problem, error) {
	known := make(map[string]struct{}, len(c.fields))

	var entries []entry
	for _, f := range c.fields {
		name := s.prefix + "_" + f.env
		known[name] = struct{}{}

		if raw := os.Getenv(name); raw != "" {
			entries = append(entries, entry{key: f.key, raw: raw, text: true, origin: "env " + name})
		}
	}

	names := slices.Sorted(maps.Keys(known))

	var problems []Problem
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, found := known[name]; found || c.only || !strings.HasPrefix(name, s.prefix+"_") || slices.Contains(s.reserved, name) {
			continue
		}

		p := Problem{Code: CodeUnknownKey, Origin: "env " + name, Message: name + " is not a known setting"}
		if near := suggest(name, names); near != "" {
			p.Hint = "did you mean " + near + "?"
		}

		problems = append(problems, p)
	}

	return entries, problems, nil
}
