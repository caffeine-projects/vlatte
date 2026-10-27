package handler_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/match/matchscenario"
)

func TestResetTool(t *testing.T) {
	// A reset starts a test over: the mocks the admin API added go, and the scenarios start again.
	added := newFake("added")
	added.origin = adminapi.Origin
	store := newStore(t, added, newFake("from-code"))
	scenarios := matchscenario.New()
	scenarios.SetScenario("checkout", "paid")
	cs := connect(t, store, adminapi.Capabilities{Scenarios: scenarios})

	require.Equal(t, "done", call(t, cs, "reset", nil))
	require.Equal(t, []string{"from-code"}, ids(t, store))
	require.Equal(t, map[string]string{"checkout": "started"}, scenarios.Scenarios())
}

func TestResetTool_FilesThatFailToLoad(t *testing.T) {
	files := &fakeFiles{err: errors.New("loader: pattern [: syntax error in pattern")}
	cs := connect(t, newStore(t), adminapi.Capabilities{Files: files})

	callFails(t, cs, "reset", nil, http.StatusInternalServerError, "load_failed")
}
