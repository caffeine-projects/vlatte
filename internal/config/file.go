package config

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if !isFormat(ext) {
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

	return walkDoc(c, data, ext, origin, s.pathKey, "a config file")
}

// walkDoc decodes data, a document in the format of ext, and turns its settings into entries.
// noun names the document in the problem of a document that isn't an object, such as "a config file".
func walkDoc(c *catalog, data []byte, ext, origin, pathKey, noun string) ([]entry, []Problem, error) {
	doc, err := decode(data, ext)
	if err != nil {
		if se, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
			line, col := position(data, se.ByteOffset)
			origin = fmt.Sprintf("%s:%d:%d", origin, line, col)
		}

		return nil, []Problem{{Code: CodeParseError, Origin: origin, Message: err.Error()}}, nil
	}

	if doc == nil {
		return nil, nil, nil
	}

	settings, ok := doc.(map[string]any)
	if !ok {
		return nil, []Problem{{Code: CodeParseError, Origin: origin, Message: noun + " holds an object of settings, not " + Describe(doc)}}, nil
	}

	w := walker{catalog: c, origin: origin, pathKey: pathKey}
	w.walk(settings, "")

	return w.entries, w.problems, nil
}

// walker turns the decoded objects of a file into entries.
type walker struct {
	*catalog

	origin   string
	pathKey  string
	outer    string // the key of the object being walked, such as "response"; empty for a document
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
			w.problem(CodeUnknownKey, key, w.full(key)+" is written as a dotted key", "nest it: "+nested(key))
		case f != nil && key == w.pathKey:
			w.problem(CodeNotAllowed, key, w.full(key)+" can't be set inside a config file", "set it with a flag or an environment variable")
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
				w.problem(CodeInvalidValue, key, fmt.Sprintf("%s must be an object, not %s", w.full(key), Describe(raw)),
					"its keys are "+strings.Join(w.children(key), ", "))

				continue
			}

			w.walk(obj, key)
		default:
			hint := ""
			if near := suggest(key, w.Keys()); near != "" {
				hint = "did you mean " + w.full(near) + "?"
			} else if prefix != "" || w.outer != "" {
				hint = "the keys under " + w.full(prefix) + " are " + strings.Join(w.children(prefix), ", ")
			}

			w.problem(CodeUnknownKey, key, w.full(key)+" is not a known setting", hint)
		}
	}
}

// problem records a problem of key, a key of the schema.
func (w *walker) problem(code, key, message, hint string) {
	w.problems = append(w.problems, Problem{Code: code, Origin: w.origin, Key: w.full(key), Message: message, Hint: hint})
}

// full writes key, a key of the schema, as the key users see: under the key of the object being walked.
func (w *walker) full(key string) string {
	return under(w.outer, key)
}

// under writes key under prefix: under("response", "status") is "response.status".
func under(prefix, key string) string {
	switch {
	case prefix == "":
		return key
	case key == "":
		return prefix
	default:
		return prefix + "." + key
	}
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
