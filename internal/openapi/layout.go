package openapi

import (
	"slices"
	"strings"
)

// File is a mock file: its name, in the folder it is written to, and its mocks, in order. It is the copy of the spec
// instead when it has no mocks.
type File struct {
	// Name is the name of the file, which ends in .mock.yaml, or .openapi.yaml for the copy of the spec.
	Name string

	// Mocks are the mocks of the file, one YAML document each.
	Mocks []Mock

	// Data is the content of a file without mocks, the copy of the spec, as data.
	Data any
}

// Layout lays mocks out in files. Every mock must be in one file.
type Layout func(mocks []Mock) []File

// PerMock lays each mock out in a file of its own, named after its ID, in ID order.
func PerMock(mocks []Mock) []File {
	files := make([]File, len(mocks))
	for i, m := range mocks {
		files[i] = File{Name: m.ID + ".mock.yaml", Mocks: []Mock{m}}
	}

	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Name, b.Name) })

	return files
}
