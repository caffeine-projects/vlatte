package mockgrpc

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/mockfile"
)

// fileLoader loads the mock files matching the server's patterns.
type fileLoader struct {
	mu    sync.Mutex
	scope *Scope   // the mocks of the last load
	saved []string // the directories of the admin API's saved mocks and of recordings, whose <id>.mock.<format> files load too
}

// load adds the mocks of every matching file, in file name order, so mocks of equal priority match in that order. A
// reload swaps them for the mocks of the last load in one step, so a call finds one set or the other. When a file has
// problems, load changes nothing and reports every file with problems. swap, when not nil, swaps more mocks in the same
// step, those of the admin API's persistent store: see [vlatte.Swap]. With no file to load and none loaded, load calls
// no swap.
func (l *fileLoader) load(app *Server, swap vlatte.Swap[*Mock]) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.loadLocked(app, swap)
}

// loadWith adds dir to the directories of saved mocks, and loads: a dir whose files fail to load is left out again.
func (l *fileLoader) loadWith(app *Server, dir string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	known := slices.Contains(l.saved, dir)
	if !known {
		l.saved = append(l.saved, dir)
	}

	err := l.loadLocked(app, nil)
	if err != nil && !known {
		l.saved = slices.DeleteFunc(l.saved, func(d string) bool { return d == dir })
	}

	return err
}

func (l *fileLoader) loadLocked(app *Server, swap vlatte.Swap[*Mock]) error {
	var files []string

	seen := make(map[string]bool)

	globs := make([]string, 0, len(app.config.MockFiles)+len(l.saved)*len(MockFileFormats()))
	for _, pattern := range app.config.MockFiles {
		if !filepath.IsAbs(pattern) && app.config.RootDir != "" {
			pattern = filepath.Join(app.config.RootDir, pattern)
		}

		globs = append(globs, pattern)
	}

	// The saved directories start at the root directory already.
	for _, dir := range l.saved {
		for _, format := range MockFileFormats() {
			globs = append(globs, filepath.Join(dir, "*.mock."+format))
		}
	}

	for _, pattern := range globs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("loader: pattern %s: %w", pattern, err)
		}

		// A relative and an absolute pattern can name one file under two names: it loads once.
		for _, file := range matches {
			key, err := filepath.Abs(file)
			if err != nil {
				key = file
			}

			if !seen[key] {
				seen[key] = true
				files = append(files, file)
			}
		}
	}

	slices.Sort(files)

	if len(files) == 0 && l.scope == nil {
		return nil
	}

	documents := mockfile.Load(files, nil)
	builders := make([]vlatte.Builder[*Mock, *Server], 0, len(documents))
	for _, doc := range documents {
		builders = append(builders, mockDoc{Document: doc})
	}

	scope, err := app.RemockWith(context.Background(), l.scope, swap, builders...)
	if err != nil {
		return fmt.Errorf("loader: %w", err)
	}

	l.scope = scope

	return nil
}

// Reload loads the mock files of Config.MockFiles again, and the mocks of the admin API's persistent store, if any:
// edited and removed files take effect, and changes made to the store directly, in one step. When a file or the store
// has problems, Reload changes nothing.
func (app *Server) Reload() error {
	return app.load()
}

// load loads the mock files, and the mocks of the admin API's persistent store, if any, all or nothing.
func (app *Server) load() error {
	if app.adminStore == nil {
		return app.loader.load(app, nil)
	}

	// A mock of the persistent store gives way to a mock file's mock of its id.
	return app.adminStore.Reload(context.Background(), func(swap vlatte.Swap[*Mock]) error {
		return app.loader.load(app, swap)
	})
}
