package vlatte

// TestingT is based on testing.T and is used for assertions.
// See Assert* methods on the application instance.
type TestingT interface {
	Helper()
	Logf(format string, a ...any)
	Error(a ...any)
	Errorf(format string, a ...any)
	Cleanup(func())
}
