// Package mockid checks the IDs of mocks, which name them in admin API paths, such as /mocks/{id}.
package mockid

import (
	"errors"
	"strings"
)

// ErrInvalid says what an ID may hold.
var ErrInvalid = errors.New("an id holds only letters, digits, '.', '_', '~' and '-'")

// Check accepts the IDs a mock can have: letters, digits, '.', '_', '~' and '-', the characters a URL path needs no
// escaping for. It returns ErrInvalid for any other, the empty one included.
func Check(id string) error {
	if id == "" || strings.ContainsFunc(id, notIDRune) {
		return ErrInvalid
	}

	return nil
}

// notIDRune reports whether r can't be part of a mock ID.
func notIDRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return false
	default:
		return !strings.ContainsRune("._~-", r)
	}
}
