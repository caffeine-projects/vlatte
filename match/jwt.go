package match

import (
	"github.com/caffeine-projects/vlatte"
)

// JWT decodes a JWT, a compact JWS whose payload is a JSON object, and matches matcher against
// {"header": {...}, "claims": {...}}, so other matchers read it: JWT(JSONPath("$.claims.sub", Equal("ana"))). The
// value is the token, or an Authorization header value with it, such as "Bearer <token>": a scheme before the token
// is skipped, whatever it is.
//
// JWT does not verify the signature, so a token anyone could have signed matches. To match only tokens signed by keys
// you trust, use [SignedJWT].
func JWT(matcher vlatte.Matcher) vlatte.Matcher {
	return &jwtMatcher{Matcher: matcher}
}
