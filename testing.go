package vlatte

// TestingT is based on testing.T and is used for assertions.
// See Assert* methods on the application instance.
type TestingT interface {
	Helper()
	Logf(format string, a ...any)
	Error(a ...any)
	Errorf(format string, a ...any)
}

// CleanupT is an interface that allows for cleanup functions to be registered.
// Usually used in testing with [testing.T].
type CleanupT interface {
	Cleanup(func())
}

type Assertions interface {
	AssertCalled(t TestingT) bool
	AssertNotCalled(t TestingT) bool
	AssertNumberOfCalls(t TestingT, expected int) bool
}
