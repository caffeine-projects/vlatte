package mockhttp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeaderNames(t *testing.T) {
	// Browsers look for the CORS headers by their standard names, and header maps are indexed by canonical names.
	tests := []struct {
		constant string
		name     string
	}{
		{HeaderContentLength, "Content-Length"},
		{HeaderAccessControlRequestMethod, "Access-Control-Request-Method"},
		{HeaderAccessControlRequestHeaders, "Access-Control-Request-Headers"},
		{HeaderAccessControlAllowOrigin, "Access-Control-Allow-Origin"},
		{HeaderAccessControlAllowMethods, "Access-Control-Allow-Methods"},
		{HeaderAccessControlAllowHeaders, "Access-Control-Allow-Headers"},
		{HeaderAccessControlAllowCredentials, "Access-Control-Allow-Credentials"},
		{HeaderAccessControlExposeHeaders, "Access-Control-Expose-Headers"},
		{HeaderAccessControlMaxAge, "Access-Control-Max-Age"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.name, tt.constant)
	}
}
