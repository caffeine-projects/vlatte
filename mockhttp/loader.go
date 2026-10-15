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
// Load runs on the first Start and on every Reload, so it replaces the mocks its last run added,
// which Server.Remock does in one step.
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
// A reload swaps them for the mocks of the last load in one step, so a request finds one set or the other.
// When a file has problems, Load changes nothing and reports every file with problems.
func (l *fileLoader) Load(app *Server) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	seen := make(map[string]struct{})

	var files []string
	for _, pattern := range app.config.MockFileSearchPatterns {
		glob := pattern
		if !filepath.IsAbs(glob) {
			glob = filepath.Join(app.config.RootDir, glob)
		}

		matches, err := filepath.Glob(glob)
		if err != nil {
			return fmt.Errorf("loader: pattern %s: %w", pattern, err)
		}

		for _, file := range matches {
			// A relative and an absolute pattern can match one file under two names: it loads once.
			key, err := filepath.Abs(file)
			if err != nil {
				key = file
			}

			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				files = append(files, file)
			}
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
	scope, err := app.Remock(context.Background(), l.scope, builders...)
	if err != nil {
		return fmt.Errorf("loader: %w", err)
	}

	l.scope = scope

	return nil
}
