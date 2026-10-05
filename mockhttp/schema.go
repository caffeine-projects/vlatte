package mockhttp

import _ "embed"

// mockSchemaJSON is the JSON schema that mock definition files must satisfy.
//
//go:embed schema/http.schema.json
var mockSchemaJSON []byte
