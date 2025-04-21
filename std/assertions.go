package std

type Assertions interface {
	AssertCalled(t TestingT) bool
	AssertNotCalled(t TestingT) bool
	AssertNumberOfCalls(t TestingT, expected int) bool
}
