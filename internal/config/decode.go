package config

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/caffeine-projects/vlatte/internal/value"
)

// _maxExactInt is the largest whole number every float64 below it holds exactly.
const _maxExactInt = 1 << 53

// isFormat reports whether ext, such as ".yaml", names a format documents can be written in.
func isFormat(ext string) bool {
	return ext == ".json" || ext == ".yaml" || ext == ".yml"
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
