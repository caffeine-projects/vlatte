package xhttp

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/caffeine-projects/vlatte/bean/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/std"
)

var SupportedExtensions = map[string]struct{}{"json": {}, "yaml": {}, "yml": {}}

var ErrUnsupportedContentType = errors.New("admin: unsupported content type")

type Encoder interface {
	Encode(io.Writer, any) error
}

type JSONEncoder struct {
}

func (j *JSONEncoder) Encode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

type Encoders map[string]Encoder

type Admin struct {
	app  *HTTPMockApp
	repo std.Store[*HTTPMock]
}

func (a *Admin) Init() http.Handler {
	r := chi.NewRouter()

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	r.Route("/mocks", func(r chi.Router) {
		r.Get("/{id}", a.get)
		r.Post("/", a.add)
	})

	return r
}

func (a *Admin) extension(r *http.Request) (string, error) {
	contentType := r.Header.Get(httpval.HeaderContentType)
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

func (a *Admin) add(w http.ResponseWriter, r *http.Request) {
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

	mock, err := BuildMockFromBytes(a.app, Request(), b, extension, false)
	if err != nil {
		http.Error(w, "", http.StatusBadRequest)
		return
	}

	err = a.repo.Save(r.Context(), mock)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add(httpval.HeaderLocation, "/mocks/"+mock.GetID())
	w.WriteHeader(http.StatusCreated)
}

func (a *Admin) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mock, err := a.repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if mock == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Add(httpval.HeaderContentType, httpval.MIMEApplicationCharsetUTF8)

	if err = json.NewEncoder(w).Encode(mock.Describe()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
