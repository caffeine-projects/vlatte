package openapi

import (
	"slices"
	"strings"
)

// File is a mock file: its name, in the folder it is written to, and its mocks, in order.
type File struct {
	// Name is the name of the file, which ends in .mock.yaml.
	Name string

	// Mocks are the mocks of the file, one YAML document each.
	Mocks []Mock
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
