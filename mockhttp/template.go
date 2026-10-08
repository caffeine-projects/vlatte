package mockhttp

import (
	"errors"
	"fmt"
)

// ErrTemplateDataKeyExists says two template data providers of a server have the same key.
var ErrTemplateDataKeyExists = errors.New("server: two template data providers have the same key")

// TemplateDataProvider adds data to the templates of HTTP mocks, which read it as .Provided.<Key>.
type TemplateDataProvider interface {
	Key() string
	Data(in *TemplateDataProviderInput) (any, error)
}

// TemplateDataProviderInput is what a TemplateDataProvider receives for each request.
type TemplateDataProviderInput struct {
	r *RequestValues
}

func (p *TemplateDataProviderInput) Request() *RequestValues {
	return p.r
}

// checkProviderKeys fails when two providers have the same key: templates could read only one of them.
func checkProviderKeys(providers []TemplateDataProvider) error {
	keys := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		if _, ok := keys[p.Key()]; ok {
			return fmt.Errorf("%w: %q", ErrTemplateDataKeyExists, p.Key())
		}

		keys[p.Key()] = struct{}{}
	}

	return nil
}
