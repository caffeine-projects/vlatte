package mockhttp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/match/matchscenario/scenariotest"
)

type customMockFileHandler struct{}

func (p *customMockFileHandler) Handle(v map[string]any, b *Builder) error {
	custom := v["custom"]
	query := custom.(map[string]any)["query"]
	key := query.(map[string]any)["key"]
	value := query.(map[string]any)["value"]

	b.Queryf(key.(string), "%s", value.(string))

	another := v["another_custom_field"]

	b.Queryf("custom", "%s", fmt.Sprintf("%v", another))

	return nil
}

func TestBuilderFs_CustomMockFileHandlers(t *testing.T) {
	m := MustNew(Setup().MockFileHandlers(&customMockFileHandler{}))
	m.MustStart()

	defer m.Close()

	m.MustMock(FromFile("testdata/builder_fs/mock_file_handler/fixture.json"))

	httpClient := &http.Client{}
	res, err := httpClient.Get(m.URL() + "/test?term=test&filter=all&page=10&q=hello-world&custom=100")
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "hi", string(b))
}

func TestBuilderFs_FromBytes(t *testing.T) {
	testCases := []struct {
		filename  string
		extension string
	}{
		{"testdata/builder_fs/from_bytes/fixture_1.yaml", "yaml"},
		{"testdata/builder_fs/from_bytes/fixture_1.json", "json"},
	}

	httpClient := &http.Client{}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			m := MustNew()
			m.MustStart()

			file, err := os.Open(tc.filename)
			require.NoError(t, err)

			b, err := io.ReadAll(file)
			require.NoError(t, err)

			m.MustMock(FromBytes(b, tc.extension))

			res, err := httpClient.Get(m.URL() + "/test")

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, http.StatusOK, res.StatusCode)

			m.Close()
			file.Close()
		})
	}
}

func TestBuilderFs_MustNotAllowMultipleReplyDefinitions(t *testing.T) {
	m := MustNew()

	filenames := []string{
		"testdata/builder_fs/multi_reply_def/1_multi_reply.yaml",
		"testdata/builder_fs/multi_reply_def/2_multi_reply.yaml",
		"testdata/builder_fs/multi_reply_def/3_multi_reply.yaml",
	}

	for _, filename := range filenames {
		t.Run(filename, func(t *testing.T) {
			s, err := m.Mock(FromFile(filename))

			require.Nil(t, s)
			require.Error(t, err)
		})
	}
}

func TestBuilderFs_InvalidSchemaValid(t *testing.T) {
	m := MustNew()
	filenames := []string{
		"testdata/builder_fs/invalid_schema/1_invalid_schema.yaml",
		"testdata/builder_fs/invalid_schema/2_no_request.yaml",
		"testdata/builder_fs/invalid_schema/3_invalid_schema.json",
	}

	for _, filename := range filenames {
		t.Run(filename, func(t *testing.T) {
			s, err := m.Mock(FromFile(filename))

			require.Nil(t, s)
			require.Error(t, err)
		})
	}
}

func TestFromBytes_ServesScenarios(t *testing.T) {
	scenariotest.Run(t, newScenarioServer(true))
}

func TestSingularMockDocuments_RejectMultipleYAMLMocks(t *testing.T) {
	data := []byte("request: {path: /first}\nresponse: {status: 200}\n---\nrequest: {path: /second}\nresponse: {status: 201}\n")
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)

	tests := []struct {
		name    string
		builder vlatte.Builder[*Mock, *Server]
		origin  string
	}{
		{name: "file", builder: FromFile(path), origin: "file " + path},
		{name: "bytes", builder: FromBytes(data, ".YAML"), origin: "bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock, err := tt.builder.Build(app)
			require.Nil(t, mock)
			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, []config.Problem{{
				Code: config.CodeParseError, Origin: tt.origin,
				Message: "expected one mock document, not 2",
				Hint:    "use MockFilePatterns to load multiple mock documents",
			}}, cfgErr.Problems)
		})
	}

	mock, err := DecodeMock(app, data, "yml")
	require.Nil(t, mock)
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []config.Problem{{
		Code: config.CodeParseError, Origin: "admin request",
		Message: "expected one mock document, not 2",
		Hint:    "use MockFilePatterns to load multiple mock documents",
	}}, cfgErr.Problems)
}

func TestSingularMockDocuments_ValidateLaterYAMLSyntax(t *testing.T) {
	data := []byte("request: {path: /first}\nresponse: {status: 200}\n---\nresponse: [")
	path := filepath.Join(t.TempDir(), "mocks.yaml")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)

	for _, builder := range []vlatte.Builder[*Mock, *Server]{FromFile(path), FromBytes(data, "yaml")} {
		mock, err := builder.Build(app)
		require.Nil(t, mock)
		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "%v", err)
		require.Len(t, cfgErr.Problems, 1)
		require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
		require.ErrorContains(t, err, "yaml: document 2:")
	}

	mock, err := DecodeMock(app, data, "yaml")
	require.Nil(t, mock)
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Len(t, cfgErr.Problems, 1)
	require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
	require.ErrorContains(t, err, "yaml: document 2:")
}

func TestFromBytes_EmptyYAMLDocumentsDoNotCountAsMocks(t *testing.T) {
	data := []byte("---\n# empty\n---\nid: only\nrequest: {path: /only}\nresponse: {status: 204}\n---\n")
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)
	mock, err := FromBytes(data, "yaml").Build(app)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.Close()) })
	require.Equal(t, "only", mock.ID())
}

func TestFromBytes_JSONRejectsTrailingDocuments(t *testing.T) {
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)
	mock, err := FromBytes([]byte(`{"request":{"path":"/first"},"response":{"status":200}} {"request":{"path":"/second"}}`), "json").Build(app)
	require.Nil(t, mock)
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Len(t, cfgErr.Problems, 1)
	require.Equal(t, config.CodeParseError, cfgErr.Problems[0].Code)
}
