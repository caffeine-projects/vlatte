package xhttp

import "github.com/caffeine-projects/vlatte"

var _ vlatte.TemplateDataProviderInput = (*TemplateDataProviderInput)(nil)

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
