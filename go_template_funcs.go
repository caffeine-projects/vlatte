package vlatte

import (
	"encoding/base64"
	json "encoding/json/v2"
	"net/url"
	"strings"
	"time"
	"uuid"
)

// goTemplateFuncs returns the functions Go templates can call besides those of text/template, but env, which the
// engine adds. Each call returns a new map, so an engine can add to its own.
func goTemplateFuncs() map[string]any {
	return map[string]any{
		"toJSON":       toJSON,
		"fromJSON":     fromJSON,
		"uuid":         newUUID,
		"now":          now,
		"base64Encode": base64Encode,
		"base64Decode": base64Decode,

		"toUpper":          strings.ToUpper,
		"toLower":          strings.ToLower,
		"trimSpace":        strings.TrimSpace,
		"title":            title,
		"upperFirst":       upperFirst,
		"truncate":         truncate,
		"replaceAll":       replaceAll,
		"split":            split,
		"join":             join,
		"substring":        substring,
		"slug":             slug,
		"regexpFind":       regexpFind,
		"regexpSubmatch":   regexpSubmatch,
		"regexpReplaceAll": regexpReplaceAll,
		"regexpMatch":      regexpMatch,
		"contains":         contains,
		"default":          defaultValue,

		"add":     add,
		"sub":     sub,
		"mul":     mul,
		"div":     div,
		"mod":     mod,
		"toInt":   toInt,
		"toFloat": toFloat,

		"list":       list,
		"append":     appendItem,
		"insert":     insertItem,
		"remove":     removeItem,
		"randomItem": randomItem,
		"shuffle":    shuffle,
		"sortBy":     sortBy,
		"reverse":    reverse,

		"jsonPath":       jsonPath,
		"jsonPathAll":    jsonPathAll,
		"deleteJSONPath": deleteJSONPath,
		"mergePatch":     mergePatch,
		"indentJSON":     indentJSON,
		"compactJSON":    compactJSON,

		"xpath":      xpathString,
		"xpathNodes": xpathNodes,

		"formatTime":   formatTime,
		"parseTime":    parseTime,
		"timeAdd":      timeAdd,
		"inZone":       inZone,
		"truncateTime": truncateTime,

		"queryUnescape": url.QueryUnescape,
		"parseQuery":    url.ParseQuery,

		"randomString": randomString,
		"randomInt":    randomInt,
		"randomFloat":  randomFloat,

		"hash": hashOf,
		"hmac": hmacOf,
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
