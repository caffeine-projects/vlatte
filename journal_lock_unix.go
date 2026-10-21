//go:build unix

package vlatte

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockDir locks dir for a journal: another journal, of this process or another, that locks it fails with
// ErrJournalInUse until the file it returns is closed. The lock goes with the process, so a crash leaves none.
func lockDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, _journalLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrJournalInUse, dir)
		}

		return nil, fmt.Errorf("journal: locking %s: %w", dir, err)
	}

	return f, nil
}
