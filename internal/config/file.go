package config

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type fileSource struct {
	pathKey string
	path    string
	dirs    []string
}

// File reads settings from a JSON or YAML file, chosen by its extension: .json, .yaml or .yml.
//
// With no dirs, path is the file to read, and it must exist.
// With dirs, path is a base name, such as ".vlatterc": the first dir that holds path with an extension
// is read, and finding none is fine. A higher-precedence source that sets pathKey, such as a --config flag,
// replaces that lookup with the file it names.
func File(pathKey, path string, dirs ...string) Source {
	return &fileSource{pathKey: pathKey, path: path, dirs: dirs}
}

func (s *fileSource) load(c *catalog, higher lookup) ([]entry, []Problem, error) {
	if len(s.dirs) == 0 {
		return s.read(c, s.path, "")
	}

	if e, ok := higher(s.pathKey); ok && s.pathKey != "" {
		path, _ := scalarText(e.raw)
		return s.read(c, path, e.origin)
	}

	for _, dir := range s.dirs {
		matches, err := s.find(dir)
		if err != nil {
			return nil, []Problem{{Code: CodeReadError, Origin: "file " + dir, Message: err.Error()}}, nil
		}

		switch len(matches) {
		case 0:
			continue
		case 1:
			return s.read(c, filepath.Join(dir, matches[0]), "")
		default:
			return nil, []Problem{{
				Code:    CodeFileAmbiguous,
				Origin:  "file " + dir,
				Message: fmt.Sprintf("found %s in %s", strings.Join(matches, " and "), dir),
				Hint:    "keep only one of them",
			}}, nil
		}
	}

	return nil, nil, nil
}

// find lists the files in dir named after the base name plus an extension.
func (s *fileSource) find(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var matches []string
	for _, file := range files {
		name := file.Name()
		if !file.IsDir() && strings.TrimSuffix(name, filepath.Ext(name)) == s.path {
			matches = append(matches, name)
		}
	}

	return matches, nil
}

// read decodes the file at path. requestedBy is the origin of the setting that named the file, if any.
func (s *fileSource) read(c *catalog, path, requestedBy string) ([]entry, []Problem, error) {
	origin := "file " + path
	fail := func(code, message, hint string) ([]entry, []Problem, error) {
		return nil, []Problem{{Code: code, Origin: origin, Message: message, Hint: hint}}, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".json" && ext != ".yaml" && ext != ".yml" {
		return fail(CodeUnsupportedFormat, fmt.Sprintf("config file %q is not JSON or YAML", path), "use a .json, .yaml or .yml file")
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if requestedBy != "" {
			origin = requestedBy
		}

		return fail(CodeFileNotFound, fmt.Sprintf("config file %q does not exist", path), relativeHint(path))
	}

	if err != nil {
		return fail(CodeReadError, err.Error(), "")
	}

	var doc any
	if ext == ".json" {
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil, nil
		}

		if err := json.Unmarshal(data, &doc); err != nil {
			if se, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
				line, col := position(data, se.ByteOffset)
				origin = fmt.Sprintf("file %s:%d:%d", path, line, col)
			}

			return fail(CodeParseError, err.Error(), "")
		}
	} else if err := yaml.Unmarshal(data, &doc); err != nil {
		return fail(CodeParseError, err.Error(), "")
	}

	if doc == nil {
		return nil, nil, nil
	}

	settings, ok := doc.(map[string]any)
	if !ok {
		return fail(CodeParseError, "a config file holds an object of settings, not "+describe(doc), "")
	}

	w := walker{catalog: c, origin: origin, pathKey: s.pathKey}
	w.walk(settings, "")

	return w.entries, w.problems, nil
}

// walker turns the decoded objects of a file into entries.
type walker struct {
	*catalog

	origin   string
	pathKey  string
	entries  []entry
	problems []Problem
}

func (w *walker) walk(settings map[string]any, prefix string) {
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		raw := settings[name]

		key := name
		if prefix != "" {
			key = prefix + "." + name
		}

		f := w.byKey[key]
		_, group := w.groups[key]

		switch {
		case strings.Contains(name, "."):
			w.problem(CodeUnknownKey, key, key+" is written as a dotted key", "nest it: "+nested(key))
		case f != nil && key == w.pathKey:
			w.problem(CodeNotAllowed, key, key+" can't be set inside a config file", "set it with a flag or an environment variable")
		case f != nil && f.section:
			if obj, ok := raw.(map[string]any); ok {
				// An object turns the section on, with its keys or its defaults.
				w.entries = append(w.entries, entry{key: key, raw: true, origin: w.origin})
				w.walk(obj, key)

				continue
			}

			w.entries = append(w.entries, entry{key: key, raw: raw, origin: w.origin})
		case f != nil:
			w.entries = append(w.entries, entry{key: key, raw: raw, origin: w.origin})
		case group:
			obj, ok := raw.(map[string]any)
			if !ok {
				w.problem(CodeInvalidValue, key, fmt.Sprintf("%s must be an object, not %s", key, describe(raw)),
					"its keys are "+strings.Join(w.children(key), ", "))

				continue
			}

			w.walk(obj, key)
		default:
			hint := ""
			if near := suggest(key, w.keys()); near != "" {
				hint = "did you mean " + near + "?"
			} else if prefix != "" {
				hint = "the keys under " + prefix + " are " + strings.Join(w.children(prefix), ", ")
			}

			w.problem(CodeUnknownKey, key, key+" is not a known setting", hint)
		}
	}
}

func (w *walker) problem(code, key, message, hint string) {
	w.problems = append(w.problems, Problem{Code: code, Origin: w.origin, Key: key, Message: message, Hint: hint})
}

// nested writes a dotted key as nested objects: log.level becomes "log: {level: ...}".
func nested(key string) string {
	parts := strings.Split(key, ".")
	s := parts[len(parts)-1] + ": ..."
	for i := len(parts) - 2; i >= 0; i-- {
		s = parts[i] + ": {" + s + "}"
	}

	return s
}

// position converts the offset of a JSON syntax error to a 1-based line and column.
// The offset is the 0-based index of the offending byte.
func position(data []byte, offset int64) (line, col int) {
	before := data[:min(max(int(offset), 0), len(data))]
	line = bytes.Count(before, []byte("\n")) + 1
	col = len(before) - bytes.LastIndexByte(before, '\n')

	return line, col
}

// relativeHint says where relative paths start, since files are looked up from the working directory.
func relativeHint(path string) string {
	if wd, err := os.Getwd(); err == nil && !filepath.IsAbs(path) {
		return "relative paths start at " + wd
	}

	return ""
}
