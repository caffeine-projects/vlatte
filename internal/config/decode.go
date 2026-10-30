package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte/internal/value"
)

// _maxExactInt is the largest whole number every float64 below it holds exactly.
const _maxExactInt = 1 << 53

// _formats are the formats documents can be written in, as Format names them.
var _formats = []string{"json", "yaml", "yml"}

// Formats returns the formats documents can be written in, as Format names them.
func Formats() []string {
	return slices.Clone(_formats)
}

// isFormat reports whether ext, such as ".yaml", names a format documents can be written in.
func isFormat(ext string) bool {
	f, ok := strings.CutPrefix(ext, ".")
	return ok && slices.Contains(_formats, f)
}

// Format normalizes the format of a document, such as ".YAML", and reports whether it is JSON or YAML.
func Format(format string) (string, bool) {
	f := strings.ToLower(strings.TrimPrefix(format, "."))
	return f, isFormat("." + f)
}

// Parse parses data, a document in format, json, yaml or yml as Format takes it, into the data model of package value,
// as decode does.
func Parse(data []byte, format string) (any, error) {
	f, ok := Format(format)
	if !ok {
		return nil, fmt.Errorf("config: format %q is not JSON or YAML", format)
	}

	return decode(data, "."+f)
}

// YAMLDocument is one nonempty document of a YAML stream.
type YAMLDocument struct {
	// Number is its 1-based position in the stream, including empty documents.
	Number int
	// Data is a standalone YAML document. A sole first document keeps the original bytes, so its diagnostics keep
	// their line numbers and formatting.
	Data []byte
}

// YAMLDocuments reads the complete YAML stream before returning its nonempty documents, in order. Implicit empty
// documents are skipped; explicit null, scalar and list values remain for the caller to validate. A failure names
// its document number and returns no documents. Each result can be parsed independently with [Parse].
func YAMLDocuments(data []byte) ([]YAMLDocument, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var documents []YAMLDocument
	for number := 1; ; number++ {
		var node yaml.Node
		err := decoder.Decode(&node)
		if errors.Is(err, io.EOF) {
			if len(documents) == 1 && documents[0].Number == 1 {
				documents[0].Data = data
			}

			return documents, nil
		}

		if err != nil {
			return nil, fmt.Errorf("yaml: document %d: %w", number, err)
		}

		if len(node.Content) == 0 {
			continue
		}

		root := node.Content[0]
		if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" && root.Value == "" && root.Style == 0 {
			continue
		}

		encoded, err := yaml.Marshal(&node)
		if err != nil {
			return nil, fmt.Errorf("yaml: encoding document %d: %w", number, err)
		}

		documents = append(documents, YAMLDocument{Number: number, Data: encoded})
	}
}

// decode parses data, a JSON or YAML document chosen by ext, into the data model of package value, one for both
// formats: objects are map[string]any, lists []any, whole numbers that fit int64 are int64, other numbers float64.
// YAML timestamps and mapping keys keep their text, as they would in JSON.
// An empty document decodes to nil.
func decode(data []byte, ext string) (any, error) {
	if ext == ".json" {
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil
		}

		return value.DecodeJSON(data)
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}

	if node.Kind == 0 || (node.Kind == yaml.DocumentNode && len(node.Content) == 0) {
		return nil, nil
	}

	if err := retag(&node); err != nil {
		return nil, err
	}

	var doc any
	if err := node.Decode(&doc); err != nil {
		return nil, err
	}

	return normalize(doc), nil
}

// retag makes timestamps and mapping keys decode as text, as JSON would give them, and floats that are whole numbers
// that fit an int64 decode as those integers, with every digit, as JSON reads them.
// A YAML 1.1 merge key (<<) keeps its meaning. A key that isn't text, such as a list, is an error.
func retag(n *yaml.Node) error {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range n.Content {
			if err := retag(child); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]

			switch {
			case key.Kind == yaml.ScalarNode && key.ShortTag() != "!!merge":
				key.Tag = "!!str"
			case key.Kind == yaml.ScalarNode:
			case key.Kind == yaml.AliasNode && key.Alias != nil && key.Alias.Kind == yaml.ScalarNode && key.Alias.ShortTag() == "!!str":
			default:
				return fmt.Errorf("yaml: line %d: a mapping key must be text", key.Line)
			}

			if err := retag(n.Content[i+1]); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!timestamp":
			n.Tag = "!!str"
		case "!!float":
			// Decoded as a float64 first, 9.007199254740993e15 would lose its last digit. YAML reads numbers without
			// their underscores, and integers with its own rules: !!float 0777 is 511, so those keep their text.
			plain := strings.ReplaceAll(n.Value, "_", "")
			if _, err := strconv.ParseInt(plain, 0, 64); err == nil {
				n.Tag = "!!int"
			} else if i, ok := value.ParseInteger(plain); ok {
				n.Tag, n.Value = "!!int", strconv.FormatInt(i, 10)
			}
		}
	}

	return nil
}

// normalize turns whole numbers of a decoded document into int64 when they fit, and the rest into float64,
// so a number means the same in JSON and YAML.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
	case int:
		return int64(x)
	case uint64:
		// YAML gives uint64 only above math.MaxInt64, where JSON gives a float64.
		return float64(x)
	case float64:
		// Every whole float64 in [-2^63, 2^63) is an int64.
		if x == math.Trunc(x) && x >= -(1<<63) && x < 1<<63 {
			return int64(x)
		}
	}

	return v
}
