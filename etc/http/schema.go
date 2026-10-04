// Package httpschema embeds the JSON schema for HTTP mock definition files.
package httpschema

import _ "embed"

// Schema is the JSON schema that HTTP mock definition files must satisfy.
//
//go:embed http.schema.json
var Schema []byte
