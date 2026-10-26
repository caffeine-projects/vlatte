package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// fakeFiles is a mock server's mock files: Reload runs onReload, if any, and returns err, and SaveMocks keeps what it
// is asked for and answers saved and saveErr.
type fakeFiles struct {
	reloads  int
	err      error
	onReload func()
	dir      string
	format   string
	saved    []string
	saveErr  error
}

func (f *fakeFiles) Reload() error {
	f.reloads++
	if f.onReload != nil {
		f.onReload()
	}

	return f.err
}

func (f *fakeFiles) SaveMocks(_ context.Context, dir, format string) ([]string, error) {
	f.dir, f.format = dir, format
	return f.saved, f.saveErr
}

func TestReloadMocks(t *testing.T) {
	files := &fakeFiles{}
	url := serve(t, newStore(t), admin.Files(files))

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/reload"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, 1, files.reloads)
}

func TestReloadMocks_Failed(t *testing.T) {
	// A reload that fails tells why: every problem of every mock file, as the loader joins them.
	fileProblem := func(file string) error {
		return fmt.Errorf("server: mock 0: %w", &config.Error{Scope: "mock", Problems: []config.Problem{{
			Code: config.CodeInvalidValue, Origin: "file " + file, Key: "response.status", Message: "bad status",
		}}})
	}

	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantCode     string
		wantProblems int
	}{
		{
			name:         "files with problems",
			err:          fmt.Errorf("loader: %w", errors.Join(fileProblem("a.yaml"), fileProblem("b.yaml"))),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "load_failed",
			wantProblems: 2,
		},
		{
			name:       "an id in use in code",
			err:        fmt.Errorf("loader: server: %w", vlatte.ErrDuplicateID),
			wantStatus: http.StatusConflict,
			wantCode:   "duplicate_id",
		},
		{
			name:       "a directory that can't be read",
			err:        errors.New("loader: pattern [: syntax error in pattern"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "load_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := serve(t, newStore(t), admin.Files(&fakeFiles{err: tt.err}))

			res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/reload"})

			doc := problem(t, res, body, tt.wantStatus, tt.wantCode)
			if tt.wantProblems > 0 {
				require.Len(t, doc["problems"], tt.wantProblems)
				require.Equal(t, fmt.Sprintf("the mock files have %d problems", tt.wantProblems), doc["detail"])
			}
		})
	}
}
