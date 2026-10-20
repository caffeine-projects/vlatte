package handler

import (
	"net/http"

	"github.com/caffeine-projects/vlatte"
)

// Reset removes the mocks the admin API added, loads the mock files again, saved ones included, and starts every
// scenario over: POST /reset. Mocks added in code stay as they are. A server without mock files, or scenarios, skips
// them.
func Reset[M vlatte.Mock](store vlatte.Store[M], files Files, scenarios Scenarios) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		mocks, err := store.PickAll(r.Context())
		if err != nil {
			fail(w, r, err)
			return
		}

		added := make([]M, 0, len(mocks))
		for _, mock := range mocks {
			if o, ok := any(mock).(interface{ Origin() string }); ok && o.Origin() == Origin {
				added = append(added, mock)
			}
		}

		if err := store.Replace(r.Context(), added); err != nil {
			fail(w, r, err)
			return
		}

		if files != nil {
			if err := files.Reload(); err != nil {
				fail(w, r, loadError(err))
				return
			}
		}

		if scenarios != nil {
			scenarios.ResetScenarios()
		}

		w.WriteHeader(http.StatusNoContent)
	})
}
