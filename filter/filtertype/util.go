package filtertype

import "github.com/caffeine-projects/vlatte"

// Success returns a successful match with no specific reason.
func Success() (bool, vlatte.Reason, error) {
	return true, vlatte.Reason(""), nil
}

// Mismatch returns a failed match with a descriptive reason string.
func Mismatch(message string) (bool, vlatte.Reason, error) {
	return false, vlatte.Reason(message), nil
}

// Error returns a failed match with an error (e.g. type/parse errors).
func Error(err error) (bool, vlatte.Reason, error) {
	return false, vlatte.Reason("error"), err
}
