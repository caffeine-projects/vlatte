package vlatte

// Kit is a toolkit that will be available for components in the mocking pipeline.
type Kit interface {
	Templating() Template

	Data() any
}
