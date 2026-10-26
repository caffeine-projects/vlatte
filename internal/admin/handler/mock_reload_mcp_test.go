package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
)

func TestReloadMocksTool(t *testing.T) {
	files := &fakeFiles{}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	require.Equal(t, "done", call(t, cs, "reload_mocks", nil))
	require.Equal(t, 1, files.reloads)
}

func TestReloadMocksTool_Failed(t *testing.T) {
	files := &fakeFiles{err: fmt.Errorf("loader: server: %w", vlatte.ErrDuplicateID)}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	callFails(t, cs, "reload_mocks", nil, http.StatusConflict, "duplicate_id")
}
