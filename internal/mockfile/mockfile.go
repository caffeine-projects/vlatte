// Package mockfile reads mock documents and preserves their source while splitting YAML streams.
package mockfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// Document is a mock's source data, or an error [Load] retains for a server to report with its other build errors.
type Document struct {
	// Origin identifies the input in problems, including a YAML document number after splitting.
	Origin string
	// Source is the source identifier recorded on the mock, such as its file path or admin origin.
	Source string
	// Format selects the JSON or YAML decoder.
	Format string
	// Dir is where relative body files open. Empty uses the working directory.
	Dir string
	// Data holds the source document, before schema validation or templating.
	Data []byte
	// Err retains a read or parse failure from Load for the protocol's build pass.
	Err error
}

type builder[M, A any] struct {
	read  func() (Document, error)
	build func(Document, A) (M, error)
	hint  string
}

// Build reads a fresh document and builds one mock. A failed read or validation invokes no protocol builder.
func (b builder[M, A]) Build(app A) (M, error) {
	doc, err := b.read()
	if err == nil {
		doc, err = doc.Single(b.hint)
	}

	if err != nil {
		var zero M
		return zero, err
	}

	return b.build(doc, app)
}

// File defers reading path until each Build, and then builds its single document with build.
func File[M, A any](path string, missingHint func(string) string, multiHint string, build func(Document, A) (M, error)) builder[M, A] {
	return builder[M, A]{read: func() (Document, error) { return Read(path, missingHint) }, build: build, hint: multiHint}
}

// Read reads a JSON or YAML mock file. missingHint optionally explains how to find a missing file.
// Relative body files start at the returned Dir. Failures are [config.Error] values with scope mock.
func Read(path string, missingHint func(string) string) (Document, error) {
	origin := "file " + path
	format, ok := config.Format(filepath.Ext(path))
	if !ok {
		return Document{}, problem(config.Problem{
			Code:    config.CodeUnsupportedFormat,
			Origin:  origin,
			Message: fmt.Sprintf("mock file %q is not JSON or YAML", path),
			Hint:    "use a .json, .yaml or .yml file",
		})
	}

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		hint := ""
		if missingHint != nil {
			hint = missingHint(path)
		}

		return Document{}, problem(config.Problem{
			Code:    config.CodeFileNotFound,
			Origin:  origin,
			Message: fmt.Sprintf("mock file %q does not exist", path),
			Hint:    hint,
		})
	case err != nil:
		return Document{}, problem(config.Problem{Code: config.CodeReadError, Origin: origin, Message: err.Error()})
	}

	return Document{Origin: origin, Source: path, Format: format, Dir: filepath.Dir(path), Data: data}, nil
}

// Load reads files in their given order and expands their YAML documents. A failed file contributes one Document
// with Err set, so servers can report every bad file while keeping their existing all-or-nothing build path.
func Load(paths []string, missingHint func(string) string) []Document {
	documents := make([]Document, 0, len(paths))
	for _, path := range paths {
		doc, err := Read(path, missingHint)
		var parts []Document
		if err == nil {
			parts, err = doc.Split()
		}

		if err != nil {
			documents = append(documents, Document{Err: err})
			continue
		}

		documents = append(documents, parts...)
	}

	return documents
}

// Builder defers building this single document until Build supplies the server.
func (doc Document) Builder[M, A any](multiHint string, build func(Document, A) (M, error)) builder[M, A] {
	return builder[M, A]{read: func() (Document, error) { return doc, nil }, build: build, hint: multiHint}
}

// Decode loads the document into dst, then adds check's cross-field problems when parsing succeeded. It preserves
// schema problems and their order; a retained file error passes through before schema loading.
func (doc Document) Decode[T any](schema *config.Schema[T], dst *T, check func(string, []config.Problem) []config.Problem) error {
	if doc.Err != nil {
		return doc.Err
	}

	var problems []config.Problem
	if _, err := schema.Load(dst, []config.Source{config.Bytes(doc.Origin, doc.Format, doc.Data)}); err != nil {
		cfgErr, ok := errors.AsType[*config.Error](err)
		if !ok {
			return err
		}

		problems = cfgErr.Problems
	}

	if !slices.ContainsFunc(problems, func(p config.Problem) bool {
		return p.Code == config.CodeParseError || p.Code == config.CodeUnsupportedFormat
	}) {
		problems = append(problems, check(doc.Origin, problems)...)
	}

	if len(problems) > 0 {
		return &config.Error{Scope: "mock", Problems: problems}
	}

	return nil
}

// Single selects one nonempty document, or leaves an empty stream for the mock schema to validate. hint tells the
// caller how to load multiple documents when that is what they supplied.
func (doc Document) Single(hint string) (Document, error) {
	documents, err := doc.Split()
	if err != nil {
		return Document{}, err
	}

	if len(documents) > 1 {
		return Document{}, problem(config.Problem{
			Code:    config.CodeParseError,
			Origin:  doc.Origin,
			Message: fmt.Sprintf("expected one mock document, not %d", len(documents)),
			Hint:    hint,
		})
	}

	if len(documents) == 1 {
		return documents[0], nil
	}

	return doc, nil
}

// Split expands YAML documents in order, retaining source paths and physical document numbers in their origins.
// JSON and unsupported formats pass through for the mock schema to validate. Empty YAML documents add no mocks.
func (doc Document) Split() ([]Document, error) {
	format, ok := config.Format(doc.Format)
	if !ok || format == "json" {
		return []Document{doc}, nil
	}

	parsed, err := config.YAMLDocuments(doc.Data)
	if err != nil {
		return nil, problem(config.Problem{Code: config.CodeParseError, Origin: doc.Origin, Message: err.Error()})
	}

	documents := make([]Document, 0, len(parsed))
	for _, part := range parsed {
		next := doc
		next.Format = format
		next.Data = part.Data
		if len(parsed) > 1 || part.Number != 1 {
			next.Origin = fmt.Sprintf("%s document %d", doc.Origin, part.Number)
		}

		documents = append(documents, next)
	}

	return documents, nil
}

func problem(p config.Problem) error {
	return &config.Error{Scope: "mock", Problems: []config.Problem{p}}
}
