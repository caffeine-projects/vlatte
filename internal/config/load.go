package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// entry is a raw value a source found for a key.
type entry struct {
	key    string
	raw    any    // a value decoded from a document, or text from env and args
	text   bool   // raw is text from env or args, which coerce parses
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
	return s.load(&s.catalog, dst, sources)
}

// LoadOnly is like Load, but reads only the settings of the schema from sources that hold others too, such as those
// of another schema: an unknown key, environment variable or flag is no problem, and is skipped. A flag it doesn't
// know is skipped with the argument that follows it, unless that argument is a flag: an unknown flag takes its value
// after =, or as the next argument.
func (s *Schema[T]) LoadOnly(dst *T, sources []Source) (map[string]Value, error) {
	only := s.catalog
	only.only = true

	return s.load(&only, dst, sources)
}

func (s *Schema[T]) load(c *catalog, dst *T, sources []Source) (map[string]Value, error) {
	layers := make([][]entry, len(sources))
	var problems []Problem

	// Load the highest precedence first, so a file source can see a path set by flags or env.
	for i := len(sources) - 1; i >= 0; i-- {
		higher := layers[i+1:]

		entries, found, err := sources[i].load(c, func(key string) (entry, bool) { return find(higher, key) })
		if err != nil {
			return nil, err
		}

		layers[i] = entries
		problems = append(problems, found...)
	}

	values := make(map[string]Value)
	for _, entries := range layers {
		problems = append(problems, s.apply(dst, entries, values, "")...)
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
// prefix is the key the entries sit under, such as "response", or empty for a document.
func (s *Schema[T]) apply(dst *T, entries []entry, values map[string]Value, prefix string) []Problem {
	var problems []Problem

	// Lookups use the schema key of each value; problems and messages use the key users see.
	type setting struct {
		f *field
		v Value
	}

	converted := make([]setting, 0, len(entries))
	for _, e := range entries {
		f := s.byKey[e.key]

		v, found := coerce(f, e, under(prefix, e.key))
		if len(found) > 0 {
			problems = append(problems, found...)
			continue
		}

		converted = append(converted, setting{f: f, v: v})
	}

	// Turning a section off while setting one of its keys in the same source is contradictory.
	conflicts := make(map[string]struct{})
	for _, toggle := range converted {
		if !toggle.f.section || toggle.v.Bool {
			continue
		}

		for _, other := range converted {
			if strings.HasPrefix(other.f.key, toggle.f.key+".") {
				problems = append(problems, Problem{
					Code:    CodeConflict,
					Origin:  toggle.v.Origin,
					Key:     toggle.v.Key,
					Value:   toggle.v.Raw,
					Message: fmt.Sprintf("%s is false, but %s sets %s in the same source", toggle.v.Key, other.v.Origin, other.v.Key),
					Hint:    "remove one of them",
				})
				conflicts[toggle.f.key] = struct{}{}

				break
			}
		}
	}

	for _, c := range converted {
		if inConflict(c.f.key, conflicts) {
			continue
		}

		if set := s.sets[c.f.key]; set != nil {
			if err := set(dst, c.v); err != nil {
				problems = append(problems, problemsOf(c.v, err)...)
				continue
			}
		}

		values[c.f.key] = c.v
	}

	return problems
}

// problemsOf turns the error a Set returned for v into problems. An *Error, such as one from Decode,
// keeps its own problems; any other error is a problem of v.
func problemsOf(v Value, err error) []Problem {
	if nested, ok := errors.AsType[*Error](err); ok {
		return nested.Problems
	}

	return []Problem{{
		Code:    CodeInvalidValue,
		Origin:  v.Origin,
		Key:     v.Key,
		Value:   display(v.Raw),
		Message: fmt.Sprintf("%s=%q: %v", v.Key, display(v.Raw), err),
	}}
}

// inConflict reports whether key is a conflicting section or one of its keys.
func inConflict(key string, conflicts map[string]struct{}) bool {
	for section := range conflicts {
		if key == section || strings.HasPrefix(key, section+".") {
			return true
		}
	}

	return false
}
