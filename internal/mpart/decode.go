package mpart

import (
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/caffeine-projects/vlatte/internal/config"
)

type (
	specField = config.Field[Spec]
	partField = config.Field[partDef]
)

// partDef is a decoded part.
type partDef struct {
	part *PartSpec
}

// SpecSchema decodes the multipart object of a mock file, as response.multipart and a webhook's multipart parameter
// write it. PartSchema decodes each of its parts.
var SpecSchema = config.NewSchema(
	specField{
		Key: "type", Kind: config.String, Usage: "Subtype of the body, such as form-data, mixed, related or x-mixed-replace.",
		Set: func(s *Spec, v config.Value) error { s.Subtype = v.Str; return nil },
	},
	specField{
		Key: "boundary", Kind: config.String, Usage: "Boundary between the parts. Random when left out.",
		Set: func(s *Spec, v config.Value) error {
			if v.Str == "" {
				return config.Invalid(v, v.Key+" is empty", "leave it out for a random boundary")
			}

			s.Boundary = v.Str

			return nil
		},
	},
	specField{
		Key: "params", Kind: config.Any, Usage: "More parameters of the Content-Type, by name, such as type and start.",
		Set: func(s *Spec, v config.Value) error {
			params := make(map[string]string)
			err := config.Members(v, func(name string, member config.Value) error {
				text, ok := member.Any.(string)
				if !ok {
					return config.Mismatch(member, "a string", config.String)
				}

				params[name] = text

				return nil
			})

			s.Params = params

			return err
		},
	},
	specField{
		Key: "parts", Kind: config.Any, Usage: "Parts of the body, in order.",
		Set: func(s *Spec, v config.Value) error {
			if list, ok := v.Any.([]any); ok && len(list) == 0 {
				return config.Invalid(v, v.Key+" is empty", "add at least one part")
			}

			// A part that fails to decode keeps its place, so the checks name the parts as the file does.
			return config.Items(v, func(item config.Value) error {
				part, err := decodePart(item)
				s.Parts = append(s.Parts, part)

				return err
			})
		},
	},
)

// PartSchema decodes a part of a multipart object.
var PartSchema = config.NewSchema(
	partField{
		Key: "name", Kind: config.String, Usage: "Name of the part, in its Content-Disposition: required in form-data.",
		Set: func(d *partDef, v config.Value) error { d.part.Name = v.Str; return nil },
	},
	partField{
		Key: "filename", Kind: config.String, Usage: "File name of the part, in its Content-Disposition.",
		Set: func(d *partDef, v config.Value) error { d.part.Filename = v.Str; return nil },
	},
	partField{
		Key: "content_type", Kind: config.String,
		Usage: "Content-Type of the part. JSON bodies default to application/json, and parts with a filename to application/octet-stream.",
		Set:   func(d *partDef, v config.Value) error { d.part.ContentType = v.Str; return nil },
	},
	partField{
		Key: "header", Kind: config.Any, Usage: "More headers of the part, by name: text or a list of text each.",
		Set: func(d *partDef, v config.Value) error { return headers(v, &d.part.Header) },
	},
	partField{
		Key: "body", Kind: config.Any, Usage: "Body of the part: text as is, anything else as JSON.",
		Set: func(d *partDef, v config.Value) error {
			switch x := v.Any.(type) {
			case nil:
				return config.Invalid(v, v.Key+" is null", `remove it, or write "" for an empty body`)
			case string:
				d.part.Body, d.part.Kind = []byte(x), BodyText
			default:
				b, err := json.Marshal(x, json.Deterministic(true))
				if err != nil {
					return err
				}

				d.part.Body, d.part.Kind = b, BodyJSON
			}

			return nil
		},
	},
	partField{
		Key: "body_base64", Kind: config.String,
		Usage: "Body of the part: bytes in standard base64, for a body that isn't UTF-8 text, such as an image. Line breaks are ignored.",
		Set: func(d *partDef, v config.Value) error {
			b, err := base64.StdEncoding.DecodeString(v.Str)
			if err != nil {
				return config.Invalid(v, v.Key+" isn't standard base64: "+err.Error(), "encode the bytes with base64, padding included")
			}

			d.part.Body, d.part.Kind = b, BodyBytes

			return nil
		},
	},
	partField{
		Key: "body_file", Kind: config.String,
		Usage: "File to read the body from, at each use. Relative paths start at the mock file's directory.",
		Set: func(d *partDef, v config.Value) error {
			if v.Str == "" {
				return errors.New("expected a file name")
			}

			d.part.File = v.Str

			return nil
		},
	},
	partField{
		Key: "delay", Kind: config.Duration,
		Usage: "How long a reply waits before writing the part: milliseconds or a Go duration (1.5s).",
		Set:   func(d *partDef, v config.Value) error { d.part.Delay = v.Dur; return nil },
	},
)

// Decode decodes the multipart object v of a mock file and checks it. Every problem is keyed under v.Key, as
// response.multipart.parts[1].name, and each key gets one problem at most. With problems, the spec is what decoded,
// so a caller can check more of it at once; it is nil when v isn't an object.
func Decode(v config.Value) (*Spec, error) {
	s := new(Spec)

	err := SpecSchema.Decode(s, v)

	obj, isObject := v.Any.(map[string]any)
	if !isObject {
		return nil, err
	}

	problems := config.ProblemsOf(v, err)
	if _, ok := obj["parts"]; !ok {
		problems = append(problems, config.Problem{
			Code: config.CodeMissingValue, Origin: v.Origin, Key: v.Key + ".parts",
			Message: v.Key + ".parts is required", Hint: "add at least one part",
		})
	}

	for _, issue := range s.Check() {
		key := under(v.Key, issue.Path)
		if covered(problems, key) {
			continue
		}

		problems = append(problems, config.Problem{
			Code: issue.Code, Origin: v.Origin, Key: key, Message: issue.Text(v.Key), Hint: issue.Hint,
		})
	}

	if len(problems) > 0 {
		return s, &config.Error{Problems: problems}
	}

	return s, nil
}

// decodePart decodes a part. It returns the part even when it has problems. A part takes one body at most: each
// body key after the first conflicts with it.
func decodePart(v config.Value) (*PartSpec, error) {
	d := partDef{part: new(PartSpec)}

	err := PartSchema.Decode(&d, v)

	obj, _ := v.Any.(map[string]any)

	var problems []config.Problem
	first := ""
	for _, key := range []string{"body", "body_base64", "body_file"} {
		if _, ok := obj[key]; !ok {
			continue
		}

		if first == "" {
			first = key
			continue
		}

		problems = append(problems, config.Problem{
			Code: config.CodeConflict, Origin: v.Origin, Key: v.Key + "." + key,
			Message: fmt.Sprintf("%s.%s is set with %s.%s", v.Key, key, v.Key, first), Hint: "keep one of them",
		})
	}

	if len(problems) == 0 {
		return d.part, err
	}

	return d.part, &config.Error{Problems: append(config.ProblemsOf(v, err), problems...)}
}

// headers decodes an object of headers: a string, or a list of strings, each.
func headers(v config.Value, dst *[]Header) error {
	return config.Members(v, func(name string, member config.Value) error {
		switch x := member.Any.(type) {
		case string:
			*dst = append(*dst, Header{Name: name, Value: x})
		case []any:
			return config.Items(member, func(item config.Value) error {
				text, ok := item.Any.(string)
				if !ok {
					return config.Mismatch(item, "a string", config.String)
				}

				*dst = append(*dst, Header{Name: name, Value: text})

				return nil
			})
		default:
			return config.Mismatch(member, "a string or a list of strings", config.String)
		}

		return nil
	})
}

// covered reports whether key, or a key it is under, already has a problem.
func covered(problems []config.Problem, key string) bool {
	return slices.ContainsFunc(problems, func(p config.Problem) bool {
		return p.Key == key || strings.HasPrefix(key, p.Key+".") || strings.HasPrefix(key, p.Key+"[")
	})
}
