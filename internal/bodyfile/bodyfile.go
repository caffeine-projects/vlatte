// Package bodyfile opens the files bodies are read from, such as a reply's body_file. A body file name can be a
// template, and a name rendered from request data must not reach outside the directory the mock's author chose.
package bodyfile

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
)

// Open opens the body file name, which is what source rendered to: the same text when source isn't a template, or
// has no actions. A relative name starts at dir; "" is the working directory.
//
// A name written as it is opens as it is. A name that differs from its source came from template data, such as the
// request. The leading path elements it shares with its source were written by the mock's author, and name the
// directory the rest must stay inside: the rest opens through os.Root, which refuses absolute names, ".." that
// leaves the directory, and symbolic links that point out of it. So "files/{{ .Request.URL.Query.Get "f" }}" opens
// files in dir/files only.
func Open(dir, source, name string) (*os.File, error) {
	if name == source {
		if dir != "" && !filepath.IsAbs(name) {
			name = filepath.Join(dir, name)
		}

		return os.Open(name)
	}

	written, rendered := splitWritten(source, name)
	if !filepath.IsAbs(written) {
		written = filepath.Join(dir, written)
	}

	return os.OpenInRoot(cmp.Or(written, "."), rendered)
}

// splitWritten splits name, which source rendered to, into its leading path elements that equal those of source,
// and the rest, which holds at least the last element.
func splitWritten(source, name string) (written, rest string) {
	src := strings.Split(filepath.ToSlash(source), "/")
	got := strings.Split(filepath.ToSlash(name), "/")

	n := 0
	for n < len(src)-1 && n < len(got)-1 && src[n] == got[n] {
		n++
	}

	written = strings.Join(got[:n], "/")
	if n > 0 && written == "" {
		written = "/" // the root of an absolute name, whose first element is empty
	}

	return filepath.FromSlash(written), filepath.FromSlash(strings.Join(got[n:], "/"))
}

// Path returns the body file name joined to dir, where a relative name opens from, such as the mock file's
// directory: it opens the same file from the working directory, as a description loaded back opens it. The name stays
// as it is, since it can be a template.
func Path(dir, name string) string {
	if dir == "" || name == "" || filepath.IsAbs(name) {
		return name
	}

	return filepath.Clean(dir) + string(filepath.Separator) + name
}

// FromDir returns the path that leads from dir to the working directory, such as ../.., or "" when they are one.
func FromDir(dir string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(abs, wd)
	if err != nil || rel == "." {
		return "", err
	}

	return rel, nil
}

// Relocate makes the body_file of obj, an object of a mock's description, which opens from the working directory when
// it is relative, open from the directory toWD leads from to it, as FromDir returns: it puts toWD before it. The name
// stays as it is otherwise, since it can be a template.
func Relocate(obj any, toWD string) {
	m, _ := obj.(map[string]any)
	if name, ok := m["body_file"].(string); ok && toWD != "" && name != "" && !filepath.IsAbs(name) {
		m["body_file"] = toWD + string(filepath.Separator) + name
	}
}
