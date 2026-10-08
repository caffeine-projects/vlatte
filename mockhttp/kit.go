package mockhttp

import (
	"github.com/caffeine-projects/vlatte"
)

var _ vlatte.Kit = (*Kit)(nil)

// Kit is the vlatte.Kit of HTTP mocks: the server's template engine, and the template data of the request.
type Kit struct {
	engine vlatte.TemplateEngine
	data   any
}

func (k *Kit) TemplateEngine() vlatte.TemplateEngine {
	return k.engine
}

func (k *Kit) Data() any {
	return k.data
}
