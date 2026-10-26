package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestSaveMocksTool(t *testing.T) {
	files := &fakeFiles{saved: []string{"mocks/hello.mock.json"}}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	doc := conformsAs(t, call(t, cs, "save_mocks", map[string]any{"dir": "mocks", "format": "json"}), "json", "SavedFiles")
	require.Equal(t, []any{"mocks/hello.mock.json"}, doc["files"])
	require.Equal(t, "mocks", files.dir)
	require.Equal(t, "json", files.format)
}

func TestSaveMocksTool_Refused(t *testing.T) {
	files := &fakeFiles{}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	callFails(t, cs, "save_mocks", map[string]any{"dir": "../x"}, http.StatusBadRequest, "invalid_body")
	require.Empty(t, files.dir, "nothing was saved")
}

func TestSaveMocksTool_Defaults(t *testing.T) {
	// No arguments take the server's defaults, as an empty body does.
	files := &fakeFiles{saved: []string{"_mocks/hello.mock.yaml"}}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	doc := conformsAs(t, call(t, cs, "save_mocks", nil), "json", "SavedFiles")
	require.Equal(t, []any{"_mocks/hello.mock.yaml"}, doc["files"])
	require.Empty(t, files.dir)
	require.Empty(t, files.format)
}
