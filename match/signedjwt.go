package match

import (
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/jose"
)

// SignedJWT is [JWT] for tokens signed by one of keys: a JWK Set, a single JWK, or PEM public keys or certificates,
// such as a provider publishes or an app registers. A token passes only when its signature verifies with a key of
// keys, and fails otherwise:
//   - when its alg is "none" or an HMAC one, such as HS256;
//   - when its kid names no key, unless keys has keys without a kid, such as PEM ones, which are tried in turn;
//   - when a JWK names an alg other than the token's, so a token can't choose how its key is read;
//   - when its exp claim is now or past, or its nbf claim future.
//
// The algorithms are RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512 and EdDSA. SignedJWT panics when
// keys holds no public key, or holds a private one: a mock is no place for it.
func SignedJWT(keys string, matcher vlatte.Matcher) vlatte.Matcher {
	ks, err := jose.ParseKeySet([]byte(keys))
	if err != nil {
		panic(fmt.Errorf("match: signedjwt: %w", err))
	}

	return &jwtMatcher{Keys: keys, Matcher: matcher, keySet: ks}
}
