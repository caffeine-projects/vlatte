package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/caffeine-projects/vlatte"
)

// Loader loads mocks from outside the code, such as mock files.
// Load runs on the first Start and on every Reload, so it replaces the mocks its last run added,
// which Server.Remock does in one step. At Start, the server listens already, so URL is set, and serves once the loaders
// return.
type Loader interface {
	Load(app *Server) error
}

var _ Loader = (*fileLoader)(nil)

// fileLoader loads the mock files matching the server's search patterns, and those of the directories of its
// recordings.
type fileLoader struct {
	mu    sync.Mutex
	scope *Scope // the mocks of the last load
}

// Load adds the mocks of every matching file, and of every mock file of the recording directories and of those the
// admin API saved mocks into, in file name order,
// so mocks of equal priority match in that order. A reload swaps them for the mocks of the last load in one step, so a
// request finds one set or the other. When a file has problems, Load changes nothing and reports every file with
// problems.
func (l *fileLoader) Load(app *Server) error {
	return l.load(app, nil)
}

// load does what Load does. swap, when not nil, swaps more mocks in the same step, those of the admin API's persistent
// store: see [vlatte.Swap]. With no file to load and none loaded, load calls no swap.
func (l *fileLoader) load(app *Server, swap vlatte.Swap[*Mock]) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	seen := make(map[string]struct{})

	var files []string

	// A relative and an absolute pattern, or a pattern and a recording directory, can name one file under two
	// names, links included: it loads once.
	add := func(file string) {
		key, err := filepath.Abs(file)
		if err == nil {
			key, err = filepath.EvalSymlinks(key)
		}

		if err != nil {
			key = file
		}

		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			files = append(files, file)
		}
	}

	for _, pattern := range app.config.MockFileSearchPatterns {
		matches, err := filepath.Glob(app.config.Path(pattern))
		if err != nil {
			return fmt.Errorf("loader: pattern %s: %w", pattern, err)
		}

		for _, file := range matches {
			add(file)
		}
	}

	for _, dir := range app.recordDirs() {
		if err := mockFilesIn(dir, add); err != nil {
			return fmt.Errorf("loader: recordings %s: %w", dir, err)
		}
	}

	for _, dir := range app.savedDirs() {
		if err := mockFilesIn(dir, add); err != nil {
			return fmt.Errorf("loader: saved mocks %s: %w", dir, err)
		}
	}

	slices.Sort(files)

	if len(files) == 0 && l.scope == nil {
		return nil
	}

	builders := make([]vlatte.Builder[*Mock, *Server], 0, len(files))
	for _, file := range files {
		builders = append(builders, FromFile(file))
	}

	// A reload replaces the mocks of the last load, so edited and removed files take effect.
	scope, err := app.RemockWith(context.Background(), l.scope, swap, builders...)
	if err != nil {
		return fmt.Errorf("loader: %w", err)
	}

	l.scope = scope

	return nil
}

// mockFilesIn adds the mock files of dir, named <name>.mock.<format>. A dir that doesn't exist has none.
func mockFilesIn(dir string, add func(file string)) error {
	entries, err := os.ReadDir(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}

	for _, e := range entries {
		if !e.IsDir() && isMockFile(e.Name()) {
			add(filepath.Join(dir, e.Name()))
		}
	}

	return nil
}

// isMockFile reports whether a file is named as recordings name mock files: <name>.mock.<format>.
func isMockFile(name string) bool {
	for _, format := range MockFileFormats() {
		if strings.HasSuffix(name, ".mock."+format) {
			return true
		}
	}

	return false
}
