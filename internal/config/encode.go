package config

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// Encode writes v, a value of the data model, as a JSON or YAML document. format is json, yaml or yml,
// in any case and with or without a leading dot. Decoding the result as a document gives v back.
func Encode(v any, format string) ([]byte, error) {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "json":
		b, err := json.Marshal(v, json.Deterministic(true), jsontext.WithIndent("  "))
		if err != nil {
			return nil, err
		}

		return append(b, '\n'), nil
	case "yaml", "yml":
		node, err := yamlNode(v)
		if err != nil {
			return nil, err
		}

		return yaml.Marshal(node)
	default:
		return nil, fmt.Errorf("config: format %q is not JSON or YAML", format)
	}
}

// yamlNode builds the YAML node of a data model value. It doesn't use yaml.Node.Encode: that re-parses
// the encoder's own output, which fails for text starting with a line break, and it tags a "<<" key as a merge key.
func yamlNode(v any) (*yaml.Node, error) {
	switch x := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(x, 10)}, nil
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: yamlFloat(x)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x, Style: textStyle(x)}, nil
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range x {
			child, err := yamlNode(e)
			if err != nil {
				return nil, err
			}

			n.Content = append(n.Content, child)
		}

		return n, nil
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range slices.Sorted(maps.Keys(x)) {
			child, err := yamlNode(x[k])
			if err != nil {
				return nil, err
			}

			// A plain << is a merge key, so a text key << is always quoted.
			key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k, Style: textStyle(k)}
			if k == "<<" {
				key.Style = yaml.DoubleQuotedStyle
			}

			n.Content = append(n.Content, key, child)
		}

		return n, nil
	default:
		return nil, fmt.Errorf("config: %T is not a value of a document", v)
	}
}

// yamlFloat writes f as a YAML float.
func yamlFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	case math.IsNaN(f):
		return ".nan"
	default:
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
}

// textStyle picks how text is written: a literal block for readable lines, double quotes for text a block
// can't hold, and the encoder's choice for one line.
func textStyle(s string) yaml.Style {
	if !strings.Contains(s, "\n") {
		return 0
	}

	// A block's first line sets its indentation, so it can't start with a line break, a space or a tab
	// without an indentation indicator, which the encoder doesn't write. Blocks can't hold control characters either.
	if strings.HasPrefix(s, "\n") || strings.HasPrefix(s, " ") || strings.HasPrefix(s, "\t") {
		return yaml.DoubleQuotedStyle
	}

	for _, r := range s {
		if r != '\n' && r != '\t' && !unicode.IsPrint(r) {
			return yaml.DoubleQuotedStyle
		}
	}

	return yaml.LiteralStyle
}
