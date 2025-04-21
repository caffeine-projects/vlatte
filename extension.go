package vlatte

// Extension describes a component that can be registered within the mock server and used lately.
// Only one instance of a specific Extension should be registered, but, depending on the component,
// it could be used many times.
type Extension interface {
	UniqueName() string
}
