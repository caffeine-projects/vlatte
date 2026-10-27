package handler

import (
	"context"
	"net/http"
)

// HealthStatus is the answer of [Operations.Health]: GET /health.
type HealthStatus struct {
	Status string `json:"status"`
	Kind   string `json:"kind,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Health returns that the admin API serves, and what it serves: the kind of the mock server, such as http, and the
// URL it serves its mocks at.
func (o *Operations) Health() HealthStatus {
	h := HealthStatus{Status: "ok", Kind: o.kind}
	if o.url != nil {
		h.URL = o.url()
	}

	return h
}

// Ready returns the error of the mock store when it can't serve its mocks, such as when the database behind it is
// down, as a problem of status 503.
func (o *Operations) Ready(ctx context.Context) error {
	return o.mocks.health(ctx)
}

func (m *storeMocks) health(ctx context.Context) error {
	if err := m.mocks.Health(ctx); err != nil {
		return &problemError{
			status: http.StatusServiceUnavailable, code: _codeNotReady, detail: "the mock store isn't ready: " + err.Error(),
		}
	}

	return nil
}
