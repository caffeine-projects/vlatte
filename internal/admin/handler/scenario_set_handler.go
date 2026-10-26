package handler

import (
	"net/http"
)

// SetScenario moves the scenario of the path's name to the state of the request body, and answers it: PUT
// /scenarios/{name}. The mocks that require that state match next. Names are exact: Checkout isn't checkout.
func SetScenario(ops *Operations) http.Handler {
	return negotiated(func(w http.ResponseWriter, r *http.Request) {
		data, format, err := readBody(w, r)
		if err != nil {
			fail(w, r, err)
			return
		}

		s, err := ops.SetScenario(r.PathValue("name"), data, format)
		if err != nil {
			fail(w, r, err)
			return
		}

		respond(w, r, http.StatusOK, s)
	})
}
