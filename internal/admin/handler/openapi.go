package handler

import (
	_ "embed"
	"net/http"
	"sync"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// OpenAPIDocument is the OpenAPI document of the admin API, in YAML.
//
//go:embed openapi.yaml
var OpenAPIDocument []byte

// _openAPIJSON is the OpenAPI document in JSON.
var _openAPIJSON = sync.OnceValues(func() ([]byte, error) {
	doc, err := config.Parse(OpenAPIDocument, _formatYAML)
	if err != nil {
		return nil, err
	}

	return config.Encode(doc, _formatJSON)
})

// OpenAPI answers the OpenAPI document of the admin API, in JSON or YAML: GET /openapi.
func OpenAPI() http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		f, _ := responseFormat(r)
		if f.format == _formatYAML {
			write(w, http.StatusOK, f.contentType, OpenAPIDocument)
			return
		}

		body, err := _openAPIJSON()
		if err != nil {
			fail(w, r, err)
			return
		}

		write(w, http.StatusOK, f.contentType, body)
	})
}
