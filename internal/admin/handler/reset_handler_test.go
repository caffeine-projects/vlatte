package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

func TestReset(t *testing.T) {
	// A reset brings the server back to how it started, as between two tests: the mocks the admin API added go, the
	// mock files load again, and scenarios start over. Mocks of files and code stay.
	added, fromFile := newFake("added"), newFake("from-file")
	added.origin = adminapi.Origin
	fromFile.origin = "file mocks/from-file.yaml"

	store := newStore(t, added, fromFile)
	files := &fakeFiles{}
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")

	url := serve(t, store, adminapi.Capabilities{Files: files, Scenarios: scenarios})

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)
	require.Equal(t, []string{"from-file"}, ids(t, store))
	require.Equal(t, int64(1), added.closed.Load(), "a removed mock is closed")
	require.Equal(t, 1, files.reloads)
	require.Equal(t, map[string]string{"checkout": "started"}, scenarios.store.Scenarios())
}

func TestReset_KeepsWhatAPersistentStoreBringsBack(t *testing.T) {
	// The reload of a persistent store serves what it holds in place of the admin mocks it served: those come back,
	// and reset doesn't remove them, so it never deletes from the persistent store.
	added := newFake("added")
	added.origin = adminapi.Origin
	store := newStore(t, added)

	again := newFake("added")
	again.origin = adminapi.Origin
	files := &fakeFiles{onReload: func() { assert.NoError(t, store.Replace(t.Context(), []*fakeMock{added}, again)) }}

	url := serve(t, store, adminapi.Capabilities{Files: files})

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	require.Equal(t, http.StatusNoContent, res.StatusCode, body)

	got, err := store.Pick(t.Context(), "added")
	require.NoError(t, err)
	require.Same(t, again, got)
	require.Zero(t, again.closed.Load())
}

func TestReset_WithoutFilesOrScenarios(t *testing.T) {
	// A server of a protocol without mock files or scenarios still resets what it has.
	added := newFake("added")
	added.origin = adminapi.Origin
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
	added.origin = adminapi.Origin
	store := newStore(t, added)
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")

	files := &fakeFiles{err: errors.New("loader: pattern [: syntax error in pattern")}
	url := serve(t, store, adminapi.Capabilities{Files: files, Scenarios: scenarios})

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	problem(t, res, body, http.StatusInternalServerError, "load_failed")
	require.Equal(t, []string{"added"}, ids(t, store))
	require.Zero(t, added.closed.Load())
	require.Equal(t, map[string]string{"checkout": "paid"}, scenarios.store.Scenarios())
}

func TestReset_AStoreThatFailsAfterTheReload(t *testing.T) {
	// A store that can't list the mocks the reload left is the server's problem: the answer is a 500 that says why, and
	// the reset stops there, before it removes the admin API's mocks or starts the scenarios over.
	added := newFake("added")
	added.origin = adminapi.Origin
	store := &brokenStore{MemoryStore: new(vlatte.MemoryStore[*fakeMock])}
	require.NoError(t, store.Store(t.Context(), added))
	scenarios := newScenarios()
	scenarios.store.SetScenario("checkout", "paid")

	files := &fakeFiles{onReload: func() { store.pickAllErr = errors.New("store: broken") }}
	url := serve(t, store, adminapi.Capabilities{Files: files, Scenarios: scenarios})

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, "store: broken", doc["detail"])
	require.Equal(t, 1, files.reloads)

	all, err := store.MemoryStore.PickAll(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*fakeMock{added}, all)
	require.Zero(t, added.closed.Load())
	require.Equal(t, map[string]string{"checkout": "paid"}, scenarios.store.Scenarios())
}

func TestReset_AJournalThatFailsToClear(t *testing.T) {
	// A reset that can't clear the journal says so, and why: a test that went on would verify against the requests of
	// the test before.
	j := journalOf(t)
	require.NoError(t, j.Close())
	url := serve(t, newStore(t), adminapi.Capabilities{Journal: j})

	res, body := send(t, request{method: http.MethodPost, url: url + "/reset"})

	doc := problem(t, res, body, http.StatusInternalServerError, "internal")
	require.Equal(t, journal.ErrClosed.Error(), doc["detail"])
}
