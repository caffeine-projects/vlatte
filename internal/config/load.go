package config

import (
	"fmt"
	"slices"
	"strings"
)

// entry is a raw value a source found for a key.
type entry struct {
	key    string
	raw    any    // text from env and args; a decoded value from files
	origin string // such as "env VLATTE_ADDR", "flag --addr" or "file .vlatterc.json"
}

// lookup returns the value a higher-precedence source set for key.
type lookup func(key string) (entry, bool)

// Source is a place settings are read from: a file, environment variables or command-line arguments.
// Build one with File, Env or Args.
type Source interface {
	load(c *catalog, higher lookup) ([]entry, []Problem, error)
}

// Load reads the sources and applies their values to dst through each field's Set.
// Sources are listed lowest precedence first: a later source overrides an earlier one.
//
// It returns the winning value of every key a source set.
// The error is flag.ErrHelp when the arguments ask for help, or an *Error listing every problem found.
func (s *Schema[T]) Load(dst *T, sources []Source) (map[string]Value, error) {
	layers := make([][]entry, len(sources))
	var problems []Problem

	// Load the highest precedence first, so a file source can see a path set by flags or env.
	for i := len(sources) - 1; i >= 0; i-- {
		higher := layers[i+1:]

		entries, found, err := sources[i].load(&s.catalog, func(key string) (entry, bool) { return find(higher, key) })
		if err != nil {
			return nil, err
		}

		layers[i] = entries
		problems = append(problems, found...)
	}

	values := make(map[string]Value)
	for _, entries := range layers {
		problems = append(problems, s.apply(dst, entries, values)...)
	}

	if len(problems) > 0 {
		sortProblems(problems)
		return values, &Error{Problems: problems}
	}

	return values, nil
}

// find returns the value of key from the highest layer that set it.
func find(layers [][]entry, key string) (entry, bool) {
	for _, entries := range slices.Backward(layers) {
		for _, e := range slices.Backward(entries) {
			if e.key == key {
				return e, true
			}
		}
	}

	return entry{}, false
}

// apply converts the entries of one source and applies them in order, recording the values it applied.
func (s *Schema[T]) apply(dst *T, entries []entry, values map[string]Value) []Problem {
	var problems []Problem

	converted := make([]Value, 0, len(entries))
	for _, e := range entries {
		v, p := coerce(s.byKey[e.key], e)
		if p != nil {
			problems = append(problems, *p)
			continue
		}

		converted = append(converted, v)
	}

	// Turning a section off while setting one of its keys in the same source is contradictory.
	conflicts := make(map[string]bool)
	for _, toggle := range converted {
		if !s.byKey[toggle.Key].section || toggle.Bool {
			continue
		}

		for _, v := range converted {
			if strings.HasPrefix(v.Key, toggle.Key+".") {
				problems = append(problems, Problem{
					Code:    CodeConflict,
					Origin:  toggle.Origin,
					Key:     toggle.Key,
					Value:   toggle.Raw,
					Message: fmt.Sprintf("%s is false, but %s sets %s in the same source", toggle.Key, v.Origin, v.Key),
					Hint:    "remove one of them",
				})
				conflicts[toggle.Key] = true

				break
			}
		}
	}

	for _, v := range converted {
		if inConflict(v.Key, conflicts) {
			continue
		}

		if set := s.sets[v.Key]; set != nil {
			if err := set(dst, v); err != nil {
				problems = append(problems, Problem{
					Code:    CodeInvalidValue,
					Origin:  v.Origin,
					Key:     v.Key,
					Value:   display(v.Raw),
					Message: fmt.Sprintf("%s=%q: %v", v.Key, display(v.Raw), err),
				})

				continue
			}
		}

		values[v.Key] = v
	}

	return problems
}

// inConflict reports whether key is a conflicting section or one of its keys.
func inConflict(key string, conflicts map[string]bool) bool {
	for section := range conflicts {
		if key == section || strings.HasPrefix(key, section+".") {
			return true
		}
	}

	return false
}
