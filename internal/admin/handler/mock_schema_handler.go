package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// MockSchema answers the JSON Schema of the server's mock files, in JSON or YAML: GET /schema.
func MockSchema(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		schema, err := ops.MockSchema()
		if err != nil {
			fail(w, r, err)
			return
		}

		f, _ := responseFormat(r)
		if f.format == _formatYAML {
			doc, err := config.Parse(schema, _formatJSON)
			if err == nil {
				schema, err = config.Encode(doc, _formatYAML)
			}

			if err != nil {
				fail(w, r, err)
				return
			}
		}

		write(w, http.StatusOK, f.contentType, schema)
	})
}
