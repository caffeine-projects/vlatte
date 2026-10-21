package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// Reset removes the mocks the admin API added, loads the mocks of the server's sources again, its mock files and its
// persistent store, starts every scenario over, and clears the journal: POST /reset. Mocks added in code stay as they
// are. A server without mock files, scenarios, or a journal, skips them. The sources load first: when they fail to,
// nothing changes. The mocks of a persistent store come back from it, as it holds them: reset never deletes what it
// holds.
func Reset[M vlatte.Mock](store vlatte.Store[M], files Files, scenarios Scenarios, journal Journal) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mocks, err := store.PickAll(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		added := make(map[any]struct{}, len(mocks))
		for _, mock := range mocks {
			if origin(mock) == Origin {
				added[mock] = struct{}{}
			}
		}

		// A reload swaps in what the sources hold in one step: a file's mock in place of an admin mock of its id, and
		// the mocks of a persistent store in place of those it served before. Or nothing at all.
		if files != nil {
			if err := files.Reload(); err != nil {
				fail(w, r, loadError(err))
				return
			}
		}

		// The admin mocks the reload left are those no source holds.
		mocks, err = store.PickAll(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		var left []M
		for _, mock := range mocks {
			if _, ok := added[mock]; ok {
				left = append(left, mock)
			}
		}

		if len(left) > 0 {
			if err := store.Replace(r.Context(), left); err != nil {
				fail(w, r, err)
				return
			}
		}

		if scenarios != nil {
			scenarios.ResetScenarios()
		}

		if journal != nil {
			if err := journal.Clear(); err != nil {
				fail(w, r, err)
				return
			}
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
