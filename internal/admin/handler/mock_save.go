package handler

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// saveSettings is the body of POST /mocks/save.
type saveSettings struct {
	dir    string
	format string
}

// savedFiles is the answer of POST /mocks/save.
type savedFiles struct {
	Files []string `json:"files"`
}

var _saveSettingsSchema = config.NewSchema(
	config.Field[saveSettings]{
		Key: "dir", Kind: config.String,
		Usage: "Directory to write the mock files into, inside the server's root directory, which it starts at.",
		Set: func(s *saveSettings, v config.Value) error {
			// Requests choose where the server writes: never outside its root directory.
			if v.Str != "" && !filepath.IsLocal(filepath.FromSlash(v.Str)) {
				return errors.New("expected a relative path that stays inside the server's root directory")
			}

			s.dir = v.Str

			return nil
		},
	},
	config.Field[saveSettings]{
		Key: "format", Kind: config.String, Usage: "Format of the mock files: " + strings.Join(config.Formats(), ", ") + ".",
		Set: func(s *saveSettings, v config.Value) error {
			format, ok := config.Format(v.Str)
			if !ok {
				return fmt.Errorf("expected one of %s", strings.Join(config.Formats(), ", "))
			}

			s.format = format

			return nil
		},
	},
)

// SaveMocks writes the mocks the admin API added as mock files, and serves them from the files from then on, so they
// outlive the server: POST /mocks/save. A mock that can't be described as a mock file writes no file at all.
func SaveMocks(files Files) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		var settings saveSettings
		if err := decodeBody(_saveSettingsSchema, &settings, data, format); err != nil {
			fail(w, r, invalid("the body", err))
			return
		}

		paths, err := files.SaveMocks(r.Context(), settings.dir, settings.format)

		switch {
		case errors.Is(err, ErrNotDescribed):
			fail(w, r, &problemError{
				status: http.StatusInternalServerError, code: _codeDescribeFailed,
				detail: "nothing was saved: " + err.Error(), problems: configProblems(err),
			})
		case err != nil:
			fail(w, r, loadError(err))
		default:
			respond(w, r, http.StatusOK, savedFiles{Files: append(make([]string, 0, len(paths)), paths...)})
		}
	})
}
