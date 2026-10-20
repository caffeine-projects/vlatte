package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin"
	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/mfeat"
)

func TestReset(t *testing.T) {
	// A reset brings the server back to how it started, as between two tests: the mocks the admin API added go, the
	// mock files load again, and scenarios start over. Mocks of files and code stay.
	added, fromFile := newFake("added"), newFake("from-file")
	added.origin = handler.Origin
	fromFile.origin = "file mocks/from-file.yaml"

	store := newStore(t, added, fromFile)
	files := &fakeFiles{}
	scenarios := mfeat.NewScenarioStore()
	scenarios.SetScenario("checkout", "paid")

	url := serve(t, store, admin.Files(files), admin.Scenarios(scenarios))

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, []string{"from-file"}, ids(t, store))
	require.Equal(t, int64(1), added.closed.Load(), "a removed mock is closed")
	require.Equal(t, 1, files.reloads)
	require.Equal(t, map[string]string{"checkout": "started"}, scenarios.Scenarios())
}

func TestReset_WithoutFilesOrScenarios(t *testing.T) {
	// A server of a protocol without mock files or scenarios still resets what it has.
	added := newFake("added")
	added.origin = handler.Origin
	store := newStore(t, added, newFake("code"))

	url := serve(t, store)

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, []string{"code"}, ids(t, store))
}

func TestReset_FilesThatFailToLoad(t *testing.T) {
	// The files load first: when they fail to, the reset changes nothing, so the admin API's mocks still serve, open,
	// and the scenarios stay where they were. It removed and closed them, and a retry couldn't bring them back.
	added := newFake("added")
	added.origin = handler.Origin
	store := newStore(t, added)
	scenarios := mfeat.NewScenarioStore()
	scenarios.SetScenario("checkout", "paid")

	files := &fakeFiles{err: errors.New("loader: pattern [: syntax error in pattern")}
	url := serve(t, store, admin.Files(files), admin.Scenarios(scenarios))

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	problem(t, res, body, http.StatusInternalServerError, "load_failed")
	require.Equal(t, []string{"added"}, ids(t, store))
	require.Zero(t, added.closed.Load())
	require.Equal(t, map[string]string{"checkout": "paid"}, scenarios.Scenarios())
}
