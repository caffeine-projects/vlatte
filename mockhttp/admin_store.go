package mockhttp

import (
	"fmt"
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// EncodeMock describes mock in format, json, yaml or yml, as the admin API's GET /mocks/{id} does: what a persistent
// store of the admin API keeps, such as a document of a database. DecodeMock builds the mock back. Relative body files
// are named from the working directory. A mock that can't be described fails.
func EncodeMock(mock *Mock, format string) ([]byte, error) {
	f, ok := mockFileFormat(format)
	if !ok {
		return nil, fmt.Errorf("server: format %q is not one of %s", format, strings.Join(MockFileFormats(), ", "))
	}

	doc, err := value.ViaJSON(mock.Describe())
	if err != nil {
		return nil, fmt.Errorf("server: describing mock %q: %w", mock.ID(), err)
	}

	return config.Encode(doc, f)
}

// DecodeMock builds the mock data describes in format, as EncodeMock wrote it, for the server app, as a mock the admin
// API added: a reset of the server removes it, unless its store holds it still. Its relative body files open from the
// working directory. When data has problems, the error lists each.
func DecodeMock(app *Server, data []byte, format string) (*Mock, error) {
	return buildAdminMock(app, data, format)
}

// FileStore makes a persistent store of the mocks the admin API adds: a mock file each, <id>.mock.yaml in dir, as
// EncodeMock describes the mock. Only the store reads them: keep dir out of the mock file patterns, or its mocks would
// load twice. A relative dir starts at the server's root directory, and WorkDirVar at its work directory, as in
// "{workdir}/_admin". A file a person adds, edits or removes takes effect
// on the server's next reload. A mock whose description doesn't load back is refused: its file would fail every later
// start.
// A file's mock must have the ID the file's name gives, as the store finds a mock's file by its name: one that
// doesn't fails the start and the reload.
func FileStore(dir string) AdminStore {
	return func(app *Server) (vlatte.Store[*Mock], error) {
		return newFileStore(app, app.config.Path(dir), os.Remove), nil
	}
}

// newFileStore returns the store of the mock files of dir, for app, whose files remove removes.
func newFileStore(app *Server, dir string, remove func(name string) error) vlatte.Store[*Mock] {
	encode := func(mock *Mock) ([]byte, error) { return EncodeMock(mock, adminapi.FileStoreFormat) }
	decode := func(origin string, data []byte, format string) (*Mock, error) {
		return mockDoc{Origin: origin, Source: adminapi.Origin, Format: format, Data: data}.build(app)
	}

	return adminapi.NewFileStore(dir, encode, decode, remove)
}
