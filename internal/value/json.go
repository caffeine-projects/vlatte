package value

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
)

// DecodeJSON reads a JSON document the way documents are read: objects are map[string]any, lists []any,
// and a number is an int64 when its value is whole and fits, with all its digits, and a float64 otherwise.
// Duplicate names, invalid UTF-8 and numbers beyond float64 are errors; syntax errors are *jsontext.SyntacticError.
func DecodeJSON(data []byte) (any, error) {
	return decodeJSON(data, false)
}

// DecodeJSONLenient reads a JSON document as DecodeJSON does, but takes what a server may be sent:
// the last of duplicate names wins, invalid UTF-8 becomes U+FFFD and a number beyond float64 is ±Inf.
func DecodeJSONLenient(data []byte) (any, error) {
	return decodeJSON(data, true)
}

// ViaJSON returns what the JSON of v means: v written with encoding/json/v2, then read with DecodeJSON.
// Values of any Go type, such as a map[string]string or an int, come back in the data model of documents.
func ViaJSON(v any) (any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return DecodeJSON(data)
}

func decodeJSON(data []byte, lenient bool) (any, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(data), jsontext.AllowDuplicateNames(lenient), jsontext.AllowInvalidUTF8(lenient))

	doc, err := readJSON(dec, lenient)
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

// readJSON reads the next value of dec, token by token, so an integer keeps all its digits as an int64:
// read as a float64, one above 2^53 would change.
func readJSON(dec *jsontext.Decoder, lenient bool) (any, error) {
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
		return number(tok.String(), lenient)
	case jsontext.KindBeginArray:
		list := make([]any, 0)
		for dec.PeekKind() != jsontext.KindEndArray {
			v, err := readJSON(dec, lenient)
			if err != nil {
				return nil, err
			}

			list = append(list, v)
		}

		_, err := dec.ReadToken()

		return list, err
	default:
		// A value starts with one of the kinds above, or begins an object: ReadToken returns no end of an array
		// or an object where a value starts, and no invalid kind without an error.
		obj := make(map[string]any)
		for dec.PeekKind() != jsontext.KindEndObject {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}

			// A token is only valid until the next read.
			name := tok.String()

			v, err := readJSON(dec, lenient)
			if err != nil {
				return nil, err
			}

			obj[name] = v
		}

		_, err := dec.ReadToken()

		return obj, err
	}
}
