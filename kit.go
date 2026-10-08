package vlatte

// Kit is what the mock pipeline gives its components, such as event listeners.
type Kit interface {
	// TemplateEngine is the engine of the server, so a component's templates call the same functions as the
	// templates of replies.
	TemplateEngine() TemplateEngine

	// Data is the template data of the request being served.
	Data() any
}
