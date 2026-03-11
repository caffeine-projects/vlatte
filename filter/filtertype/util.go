package filtertype

// Success returns a successful match.
func Success() (bool, error) {
	return true, nil
}

// Mismatch returns a failed match (caller may use message for logging).
func Mismatch(message string) (bool, error) {
	return false, nil
}

// Error returns a failed match with an error (e.g. type/parse errors).
func Error(err error) (bool, error) {
	return false, err
}
