package mockhttp

import "github.com/caffeine-projects/vlatte"

// TemplateDataProvider adds data to the templates of HTTP mocks, under its Key.
type TemplateDataProvider interface {
	Key() string
	Data(in *TemplateDataProviderInput) (any, error)
}

// TemplateDataProviderInput is what a TemplateDataProvider receives for each request.
type TemplateDataProviderInput struct {
	s vlatte.Template
	r *RequestValues
}

func (p *TemplateDataProviderInput) Request() *RequestValues {
	return p.r
}

func (p *TemplateDataProviderInput) Templating() vlatte.Template {
	return p.s
}
