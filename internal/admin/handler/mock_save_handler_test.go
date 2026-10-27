package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestSaveMocks(t *testing.T) {
	// The server writes the files; the answer says where, so the client can commit them.
	files := &fakeFiles{saved: []string{"testdata/_mocks/hello.mock.yaml"}}
	url := serve(t, newStore(t), adminapi.Capabilities{Files: files})

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/save", contentType: "application/yaml", body: "dir: mocks\nformat: .JSON\n"})

	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.Equal(t, []any{"testdata/_mocks/hello.mock.yaml"}, conforms(t, res, body, "SavedFiles")["files"])
	require.Equal(t, "mocks", files.dir)
	require.Equal(t, "json", files.format)
}

func TestSaveMocks_Defaults(t *testing.T) {
	// Without a body, the server's defaults apply; with nothing to save, the list is empty, not null.
	files := &fakeFiles{}
	url := serve(t, newStore(t), adminapi.Capabilities{Files: files})

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/save"})

	require.Equal(t, http.StatusOK, res.StatusCode, body)
	require.JSONEq(t, `{"files": []}`, body)
	require.Empty(t, files.dir)
	require.Empty(t, files.format)
}

func TestSaveMocks_Refused(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		saveErr    error
		wantStatus int
		wantCode   string
	}{
		{name: "a format that isn't a mock file's", body: `{"format": "toml"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{name: "an unknown setting", body: `{"directory": "x"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{name: "a directory out of the root", body: `{"dir": "../x"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{name: "an absolute directory", body: `{"dir": "/etc"}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_body"},
		{
			name:       "a mock that can't be described",
			saveErr:    fmt.Errorf("%w: mock %q: invalid UTF-8", adminapi.ErrNotDescribed, "binary"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "describe_failed",
		},
		{
			name: "files that fail to load",
			saveErr: &config.Error{Problems: []config.Problem{{
				Code: config.CodeInvalidValue, Origin: "file testdata/_mocks/x.mock.yaml", Key: "response.status", Message: "bad",
			}}},
			wantStatus: http.StatusInternalServerError,
			wantCode:   "load_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := serve(t, newStore(t), adminapi.Capabilities{Files: &fakeFiles{saveErr: tt.saveErr}})

			res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/save", body: tt.body})

			doc := problem(t, res, body, tt.wantStatus, tt.wantCode)
			if tt.wantCode == "describe_failed" {
				require.Contains(t, doc["detail"], "nothing was saved: ")
			}
		})
	}
}

func TestSaveMocks_NotSupported(t *testing.T) {
	url := serve(t, newStore(t))

	res, body := send(t, request{method: http.MethodPost, url: url + "/mocks/save"})

	doc := problem(t, res, body, http.StatusNotImplemented, "not_supported")
	require.Equal(t, "this mock server can't save mock files", doc["detail"])
}
