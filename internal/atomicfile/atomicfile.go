// Package atomicfile writes files that readers find whole, or not at all.
package atomicfile

import "os"

// Write writes data to path through a temporary file of dir, named after pattern as os.CreateTemp names it, renamed
// into place: a reader finds the file whole, or not at all. A write that fails removes the temporary file. dir is on
// the file system of path, such as its directory.
func Write(dir, pattern, path string, data []byte) error {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(0o644)
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
