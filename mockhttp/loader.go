package mockhttp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/caffeine-projects/vlatte"
)

// Loader loads mocks from outside the code, such as mock files.
// Load runs on the first Start and on every Reload, so it replaces the mocks its last run added.
type Loader interface {
	Load(app *Server) error
}

var _ Loader = (*fileLoader)(nil)

// fileLoader loads the mock files matching the server's search patterns.
type fileLoader struct {
	mu    sync.Mutex
	scope *Scope // the mocks of the last load
}

// Load adds the mocks of every matching file, in file name order, so mocks of equal priority match in that order.
// It adds all of them or, when a file has problems, none, and reports every file with problems.
func (l *fileLoader) Load(app *Server) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	seen := make(map[string]struct{})

	var files []string
	for _, pattern := range app.config.MockFileSearchPatterns {
		matches, err := filepath.Glob(filepath.Join(app.config.RootDir, pattern))
		if err != nil {
			return fmt.Errorf("loader: pattern %s: %w", pattern, err)
		}

		for _, file := range matches {
			if _, ok := seen[file]; !ok {
				seen[file] = struct{}{}
				files = append(files, file)
			}
		}
	}

	slices.Sort(files)

	// A reload replaces the mocks of the last load, so edited and removed files take effect.
	if l.scope != nil {
		if err := app.Unmock(context.Background(), l.scope); err != nil {
			return fmt.Errorf("loader: %w", err)
		}

		l.scope = nil
	}

	if len(files) == 0 {
		return nil
	}

	builders := make([]vlatte.Builder[*Mock, *Server], 0, len(files))
	for _, file := range files {
		builders = append(builders, FromFile(file))
	}

	scope, err := app.Mock(builders...)
	if err != nil {
		return fmt.Errorf("loader: %w", err)
	}

	l.scope = scope

	return nil
}
