package config

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// _maxExactInt is the largest whole number every float64 below it holds exactly.
const _maxExactInt = 1 << 53

// isFormat reports whether ext, such as ".yaml", names a format documents can be written in.
func isFormat(ext string) bool {
	return ext == ".json" || ext == ".yaml" || ext == ".yml"
}

// decode parses data, a JSON or YAML document chosen by ext, into one data model for both formats:
// objects are map[string]any, lists []any, whole numbers that fit int64 are int64 and other numbers float64.
// YAML timestamps and mapping keys keep their text, as they would in JSON.
// An empty document decodes to nil.
func decode(data []byte, ext string) (any, error) {
	if ext == ".json" {
		if len(bytes.TrimSpace(data)) == 0 {
			return nil, nil
		}

		doc, err := decodeJSON(data)
		if err != nil {
			return nil, err
		}

		return normalize(doc), nil
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

// retag makes timestamps and mapping keys decode as text, as JSON would give them.
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
		if n.ShortTag() == "!!timestamp" {
			n.Tag = "!!str"
		}
	}

	return nil
}

// decodeJSON parses a JSON document token by token, so an integer keeps all its digits as an int64:
// decoded as a float64, one above 2^53 would change.
func decodeJSON(data []byte) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data))

	doc, err := readJSON(dec)
	if err != nil {
		return nil, err
	}

	// A document holds one value.
	offset := dec.InputOffset()
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}

		return nil, &jsontext.SyntacticError{ByteOffset: offset, Err: errors.New("a document holds one value, not more")}
	}

	return doc, nil
}

// readJSON reads the next value of dec.
func readJSON(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}

	switch tok.Kind() {
	case jsontext.KindNull:
		return nil, nil
	case jsontext.KindFalse, jsontext.KindTrue:
		return tok.Bool(), nil
	case jsontext.KindString:
		return tok.String(), nil
	case jsontext.KindNumber:
		return number(tok.String())
	case jsontext.KindBeginArray:
		list := make([]any, 0)
		for dec.PeekKind() != jsontext.KindEndArray {
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}

			list = append(list, v)
		}

		_, err := dec.ReadToken()

		return list, err
	case jsontext.KindBeginObject:
		obj := make(map[string]any)
		for dec.PeekKind() != jsontext.KindEndObject {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}

			// A token is only valid until the next read.
			name := tok.String()

			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}

			obj[name] = v
		}

		_, err := dec.ReadToken()

		return obj, err
	default:
		return nil, fmt.Errorf("jsontext: unexpected %s", tok.Kind())
	}
}

// number reads a JSON number: an integer as int64 when it fits, anything else as float64.
func number(text string) (any, error) {
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n, nil
	}

	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return nil, fmt.Errorf("number %s is out of range", text)
	}

	return f, nil
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
		// YAML gives uint64 only above math.MaxInt64.
		if x <= math.MaxInt64 {
			return int64(x)
		}

		return float64(x)
	case float64:
		// Every whole float64 in [-2^63, 2^63) is an int64.
		if x == math.Trunc(x) && x >= -(1<<63) && x < 1<<63 {
			return int64(x)
		}
	}

	return v
}
