package handler

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
