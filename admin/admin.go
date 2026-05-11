// Package admin provides a transport-agnostic admin API for managing mocks
// stored in a [vlatte.Store]. It exposes an [http.Handler] that can be
// mounted by any mock server that speaks HTTP for its control plane — HTTP,
// gRPC and future transports alike.
package admin

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/descriptor"
)

// SupportedExtensions lists the content extensions the admin API accepts
// when a mock definition is submitted to it.
var SupportedExtensions = map[string]struct{}{"json": {}, "yaml": {}, "yml": {}}

// ErrUnsupportedContentType is returned when a request carries a Content-Type
// that does not map to one of the [SupportedExtensions].
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
	r := chi.NewRouter()

	r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	r.Route("/mocks", func(r chi.Router) {
		r.Get("/{id}", a.get)
		r.Post("/", a.add)
	})

	return r
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

	if _, ok := SupportedExtensions[extension[0]]; !ok {
		return "", ErrUnsupportedContentType
	}

	return extension[0], nil
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add(headerLocation, "/mocks/"+mock.ID())
	w.WriteHeader(http.StatusCreated)
}

func (a *Admin[B]) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
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

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
