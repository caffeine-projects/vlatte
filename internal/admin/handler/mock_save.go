package handler

import (
	"context"
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

// SavedFiles is the answer of [Operations.SaveMocks]: POST /mocks/save.
type SavedFiles struct {
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

// SaveMocks writes the mocks the admin API added as mock files, with the settings of doc, a document in format, and
// serves them from the files from then on, so they outlive the server. It returns the files' paths. A mock that can't
// be described as a mock file writes no file at all. It needs a server with mock files.
func (o *Operations) SaveMocks(ctx context.Context, doc []byte, format string) (SavedFiles, error) {
	if o.files == nil {
		return SavedFiles{}, notSupported("save mock files")
	}

	var settings saveSettings
	if err := decodeBody(_saveSettingsSchema, &settings, doc, format); err != nil {
		return SavedFiles{}, invalid("the body", err)
	}

	paths, err := o.files.SaveMocks(ctx, settings.dir, settings.format)

	switch {
	case errors.Is(err, ErrNotDescribed):
		return SavedFiles{}, &problemError{
			status: http.StatusInternalServerError, code: _codeDescribeFailed,
			detail: "nothing was saved: " + err.Error(), problems: configProblems(err),
		}
	case err != nil:
		return SavedFiles{}, loadError(err)
	default:
		return SavedFiles{Files: append(make([]string, 0, len(paths)), paths...)}, nil
	}
}
