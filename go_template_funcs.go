package vlatte

import (
	"encoding/base64"
	json "encoding/json/v2"
	"time"
	"uuid"
)

// goTemplateFuncs returns the functions Go templates can call besides those of text/template. Each call returns a
// new map, so an engine can add to its own.
func goTemplateFuncs() map[string]any {
	return map[string]any{
		"toJson":       toJSON,
		"fromJson":     fromJSON,
		"uuid":         newUUID,
		"now":          now,
		"base64Encode": base64Encode,
		"base64Decode": base64Decode,
	}
}

// toJSON returns v as JSON, so a value goes into a JSON body escaped and, for a string, quoted.
func toJSON(v any) (string, error) {
	b, err := json.Marshal(v, json.Deterministic(true))
	return string(b), err
}

// fromJSON parses JSON text, such as a JSON request body: objects become map[string]any, lists []any and numbers
// float64.
func fromJSON(text string) (any, error) {
	var v any
	err := json.Unmarshal([]byte(text), &v)

	return v, err
}

func newUUID() string {
	return uuid.New().String()
}

// now returns the current time without its monotonic clock reading, which would print as "m=+…".
func now() time.Time {
	return time.Now().Round(0)
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return string(b), err
}
