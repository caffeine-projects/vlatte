package mockhttp

import (
	"net/http"
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

func TestHeaderNamesAreCanonical(t *testing.T) {
	// Header maps are indexed by canonical names: a constant in another form misses the header it names when it indexes
	// one, as a matcher reading the request or a test reading the response might.
	for _, name := range []string{
		HeaderIdempotencyKey,
		HeaderRetryAfter,
		HeaderETag,
		HeaderIfNoneMatch,
		HeaderIfMatch,
		HeaderExpires,
		HeaderLink,
		HeaderUserAgent,
		HeaderAcceptLanguage,
		HeaderTraceparent,
		HeaderXRateLimitLimit,
		HeaderXRateLimitRemaining,
		HeaderXRateLimitReset,
	} {
		require.Equal(t, http.CanonicalHeaderKey(name), name)
	}
}

func TestMIMEApplicationProblemJSON(t *testing.T) {
	// Clients recognize an RFC 9457 problem details body by this exact media type.
	require.Equal(t, "application/problem+json", MIMEApplicationProblemJSON)
}
