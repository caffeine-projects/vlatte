//go:build !unix

package vlatte

import "os"

// lockDir locks nothing where the journal has no lock to take: two journals of a directory are the user's to avoid.
func lockDir(string) (*os.File, error) {
	return nil, nil
}
