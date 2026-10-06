package config

import (
	"fmt"
	"strings"
)

type bytesSource struct {
	origin string
	format string
	data   []byte
}

// Bytes reads settings from a JSON or YAML document held in memory, such as a request body.
// format is json, yaml or yml, in any case and with or without a leading dot.
// origin names the document in problems, such as "file mocks/users.yaml" or "admin request".
func Bytes(origin, format string, data []byte) Source {
	return &bytesSource{origin: origin, format: format, data: data}
}

func (s *bytesSource) load(c *catalog, _ lookup) ([]entry, []Problem, error) {
	ext := "." + strings.ToLower(strings.TrimPrefix(s.format, "."))
	if !isFormat(ext) {
		return nil, []Problem{{
			Code:    CodeUnsupportedFormat,
			Origin:  s.origin,
			Value:   s.format,
			Message: fmt.Sprintf("format %q is not JSON or YAML", s.format),
			Hint:    "use json, yaml or yml",
		}}, nil
	}

	return walkDoc(c, s.data, ext, s.origin, "", "a document")
}
