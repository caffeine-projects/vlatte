package mockhttp

import "github.com/caffeine-projects/vlatte"

// TemplateDataProvider adds data to the templates of HTTP mocks, which read it as .Provided.<Key>.
type TemplateDataProvider = vlatte.TemplateDataProvider[*TemplateDataProviderInput]

// TemplateDataProviderInput is what a TemplateDataProvider receives for each request.
type TemplateDataProviderInput struct {
	r *RequestValues
}

// Request returns the request being served, its matched mock in [RequestValues.Mock].
func (p *TemplateDataProviderInput) Request() *RequestValues {
	return p.r
}
