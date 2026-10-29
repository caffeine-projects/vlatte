package match

import (
	"github.com/caffeine-projects/vlatte"
)

// JWT matches a JWT without verifying it: it checks neither the signature, so a token anyone could have signed
// matches, nor the exp and nbf claims. To match only tokens signed by keys you trust and within their lifetime, use
// [SignedJWT].
//
// JWT decodes the token, a compact JWS whose payload is a JSON object, and matches matcher against
// {"header": {...}, "claims": {...}}, so other matchers read it: JWT(JSONPath("$.claims.sub", Equal("ana"))). The
// value is the token, or an Authorization header value with it, such as "Bearer <token>": a scheme before the token
// is skipped, whatever it is.
func JWT(matcher vlatte.Matcher) vlatte.Matcher {
	return &jwtMatcher{Matcher: matcher}
}
