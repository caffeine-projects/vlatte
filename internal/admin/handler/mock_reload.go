package handler

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// ReloadMocks loads the mock files of the server again. When a file has problems, the server serves what it served
// before. It needs a server with mock files.
func (o *Operations) ReloadMocks() error {
	if o.files == nil {
		return notSupported("load mock files")
	}

	if err := o.files.Reload(); err != nil {
		return loadError(err)
	}

	return nil
}

// loadError is the problem of mock files that failed to load.
func loadError(err error) error {
	if errors.Is(err, vlatte.ErrDuplicateID) {
		return &problemError{status: http.StatusConflict, code: _codeDuplicateID, detail: err.Error()}
	}

	problems := configProblems(err)
	if len(problems) == 0 {
		return &problemError{status: http.StatusInternalServerError, code: _codeLoadFailed, detail: err.Error()}
	}

	return &problemError{
		status: http.StatusInternalServerError, code: _codeLoadFailed,
		detail: fmt.Sprintf("the mock files have %s", count(len(problems))), problems: problems,
	}
}
