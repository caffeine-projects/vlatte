package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNegotiation(t *testing.T) {
	// Answers come in the format Accept prefers, weights and wildcards included, as RFC 9110 reads it. JSON wins
	// ties, and answers a request without Accept.
	tests := []struct {
		accept      string
		wantType    string
		wantRefused bool
	}{
		{accept: "", wantType: "application/json"},
		{accept: "application/json", wantType: "application/json"},
		{accept: "application/yaml", wantType: "application/yaml"},
		{accept: "application/x-yaml", wantType: "application/yaml"},
		{accept: "text/yaml", wantType: "application/yaml"},
		{accept: "*/*", wantType: "application/json"},
		{accept: "application/*", wantType: "application/json"},
		{accept: "text/*", wantType: "application/yaml"},
		{accept: "application/json;q=0.5, application/yaml", wantType: "application/yaml"},
		{accept: "application/yaml;q=0.5, application/json;q=0.5", wantType: "application/json"},
		{accept: "application/json;q=0, */*", wantType: "application/yaml"},
		{accept: "text/html, application/xhtml+xml, application/xml;q=0.9, */*;q=0.8", wantType: "application/json"},
		{accept: "application/xml", wantRefused: true},
		{accept: "application/json;q=0, application/yaml;q=0", wantRefused: true},
	}

	url := serve(t, newStore(t))

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			res, body := send(t, request{method: http.MethodGet, url: url + "/mocks", accept: tt.accept})

			if tt.wantRefused {
				problem(t, res, body, http.StatusNotAcceptable, "not_acceptable")
				return
			}

			require.Equal(t, http.StatusOK, res.StatusCode, body)
			require.Equal(t, tt.wantType, res.Header.Get("Content-Type"))
			require.Equal(t, "Accept", res.Header.Get("Vary"))
			conforms(t, res, body, "MockList")
		})
	}
}
