package xhttp

import (
	"fmt"
	"path/filepath"
	"sync"
)

// Loader is the interface that defines custom Mock loaders.
// Usually, it is used to load external mocks, like from the file system.
type Loader interface {
	Load(app *HTTPMockApp) error
}

var _ Loader = (*fileLoader)(nil)

type fileLoader struct {
	rwMutex sync.RWMutex
}

func (l *fileLoader) Load(app *HTTPMockApp) error {
	l.rwMutex.Lock()
	defer l.rwMutex.Unlock()

	filenames := make(map[string]struct{})

	for _, pattern := range app.config.MockFileSearchPatterns {
		m, err := filepath.Glob(filepath.Join(app.config.RootDir, pattern))
		if err != nil {
			return fmt.Errorf("loader: error searching mocks with the glob pattern %s.\n%w", pattern, err)
		}

		for _, s := range m {
			if _, ok := filenames[s]; !ok {
				filenames[s] = struct{}{}
			}
		}
	}

	// Mocks are added one at a time: adding a mock holds the application lock anyway.
	for filename := range filenames {
		if _, err := app.Mock(FromFile(filename)); err != nil {
			return fmt.Errorf("loader: error adding mock.\n%w", err)
		}
	}

	return nil
}
