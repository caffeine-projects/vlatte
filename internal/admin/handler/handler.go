// Package handler holds the admin operations of a mock server, and the units of the admin API that serve them over
// HTTP. An operation, a method of Operations, works on the store the mock server serves its mocks from, or on a
// capability of the server, such as its mock files, and knows nothing of HTTP. Its unit is an http.Handler that reads
// what the operation takes from a request, and answers what it returns.
package handler

import (
	"io"

	"github.com/caffeine-projects/vlatte"
)

// MockSummary is what the admin API tells of a mock besides its description: its state.
type MockSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	Hits     int64  `json:"hits"`
	Origin   string `json:"origin,omitempty"`
}

func summarize(mock vlatte.Mock) MockSummary {
	return MockSummary{
		ID: mock.ID(), Name: mock.Name(), Priority: mock.Priority(), Enabled: mock.Enabled(), Hits: mock.Hits(),
		Origin: origin(mock),
	}
}

// origin returns where mock came from, such as adminapi.Origin or a mock file, or "" when it doesn't say.
func origin(mock vlatte.Mock) string {
	if o, ok := mock.(interface{ Origin() string }); ok {
		return o.Origin()
	}

	return ""
}

// closeMock releases what a mock the store didn't take holds, such as the HTTP client of a proxy mock.
func closeMock(mock vlatte.Mock) {
	if c, ok := mock.(io.Closer); ok {
		_ = c.Close()
	}
}
