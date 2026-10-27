// Package atomicfile writes files that readers find whole, or not at all.
package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Write writes data to path through a temporary file of dir, named after pattern as os.CreateTemp names it, renamed
// into place: a reader finds the file whole, or not at all. A write that fails removes the temporary file. dir is on
// the file system of path, such as its directory. Others may read the file.
func Write(dir, pattern, path string, data []byte) error {
	return WritePerm(dir, pattern, path, data, 0o644)
}

// WritePerm is like Write, but gives the file perm, such as 0o600 for a file only its user reads.
func WritePerm(dir, pattern, path string, data []byte, perm fs.FileMode) error {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}

	if closeErr := f.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(f.Name(), path)
	}

	if err != nil {
		os.Remove(f.Name())
	}

	return err
}

// Saved is a file a change is about to write or remove, and what it held before, which Restore puts back.
type Saved struct {
	path    string
	prev    []byte
	existed bool
}

// Backup returns what the file at path holds now: its content, or that there is none.
func Backup(path string) (Saved, error) {
	prev, err := os.ReadFile(path)

	switch {
	case err == nil:
		return Saved{path: path, prev: prev, existed: true}, nil
	case errors.Is(err, fs.ErrNotExist):
		return Saved{path: path}, nil
	default:
		return Saved{}, err
	}
}

// Restore puts files back as they were, the last changed first: it writes back what they held, through temporary
// files named after pattern, and removes those that didn't exist. It returns what it couldn't put back.
func Restore(files []Saved, pattern string) error {
	var errs []error

	for _, f := range slices.Backward(files) {
		var err error
		if f.existed {
			err = Write(filepath.Dir(f.path), pattern, f.path, f.prev)
		} else if err = os.Remove(f.path); errors.Is(err, fs.ErrNotExist) {
			err = nil
		}

		if err != nil {
			errs = append(errs, fmt.Errorf("putting %s back: %w", f.path, err))
		}
	}

	return errors.Join(errs...)
}
