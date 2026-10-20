// Package admin provides a transport-agnostic admin API for managing mocks
// stored in a [vlatte.Store]. It exposes an [http.Handler] that can be
// mounted by any mock server that speaks HTTP for its control plane — HTTP,
// gRPC and future transports alike.
package admin

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// ErrUnsupportedContentType is returned when a request carries a Content-Type
// that does not map to a mock file format.
var ErrUnsupportedContentType = errors.New("admin: unsupported content type")

const (
	headerContentType = "Content-Type"
	headerLocation    = "Location"
	contentTypeJSON   = "application/json; charset=UTF-8"
)

// Admin is a transport-agnostic admin API for managing mocks stored in a
// [vlatte.Store].
type Admin[B vlatte.Mock] struct {
	repo    vlatte.Store[B]
	builder vlatte.MockBuilder[B]
}

// New creates an [Admin] bound to the given repo and mock builder.
func New[B vlatte.Mock](repo vlatte.Store[B], builder vlatte.MockBuilder[B]) *Admin[B] {
	return &Admin[B]{repo: repo, builder: builder}
}

// Init wires the admin routes and returns the resulting [http.Handler].
func (a *Admin[B]) Init() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	mux.HandleFunc("GET /mocks/{id}", a.get)
	mux.HandleFunc("POST /mocks", a.add)
	mux.HandleFunc("POST /mocks/{$}", a.add)

	return mux
}

func (a *Admin[B]) extension(r *http.Request) (string, error) {
	contentType := r.Header.Get(headerContentType)
	if len(contentType) == 0 {
		return "json", nil
	}

	extension, err := mime.ExtensionsByType(contentType)
	if err != nil {
		return "", err
	}

	if len(extension) == 0 {
		return "", ErrUnsupportedContentType
	}

	// mime returns extensions with a leading dot, e.g. ".json".
	ext, ok := config.Format(extension[0])
	if !ok {
		return "", ErrUnsupportedContentType
	}

	return ext, nil
}

func (a *Admin[B]) add(w http.ResponseWriter, r *http.Request) {
	extension, err := a.extension(r)
	if err != nil {
		http.Error(w, ErrUnsupportedContentType.Error(), http.StatusUnsupportedMediaType)
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	mock, err := a.builder.BuildFromBytes(b, extension)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	if err := a.repo.Store(r.Context(), mock); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, vlatte.ErrDuplicateID) {
			status = http.StatusConflict
		}

		http.Error(w, err.Error(), status)

		return
	}

	w.Header().Add(headerLocation, "/mocks/"+mock.ID())
	w.WriteHeader(http.StatusCreated)
}

func (a *Admin[B]) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mock, err := a.repo.Pick(r.Context(), id)
	if errors.Is(err, vlatte.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add(headerContentType, contentTypeJSON)

	var payload any = mock
	if d, ok := any(mock).(descriptor.SelfDescriptive); ok {
		payload = d.Describe()
	}

	if err := json.MarshalEncode(jsontext.NewEncoder(w), payload, json.Deterministic(true)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
