package match

import (
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/jose"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.Matcher         = (*jwtMatcher)(nil)
	_ vlatte.SelfDescriptive = (*jwtMatcher)(nil)
)

// jwtMatcher decodes a JWT and matches its header and claims; with a key set, only once the token verifies.
type jwtMatcher struct {
	Keys    string // the keys of SignedJWT, as given; empty for JWT
	Matcher vlatte.Matcher
	keySet  *jose.KeySet
}

// Name implements vlatte.Matcher.
func (m *jwtMatcher) Name() string {
	if m.keySet == nil {
		return "JWT"
	}

	return "SignedJWT"
}

// Match implements vlatte.Matcher.
func (m *jwtMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	text, ok := value.Text(arg.Value())
	if !ok {
		return mismatch(m.Name() + "() Value is not text")
	}

	tok, err := jose.Parse(bearerToken(text))
	if err != nil {
		return mismatch(m.Name() + "() " + err.Error())
	}

	if m.keySet != nil {
		if err := m.keySet.Verify(tok); err != nil {
			return mismatch(m.Name() + "() " + err.Error())
		}

		if err := tok.CheckTime(time.Now()); err != nil {
			return mismatch(m.Name() + "() " + err.Error())
		}
	}

	doc := map[string]any{"header": tok.Header, "claims": tok.Claims}

	ok, err = m.Matcher.Match(vlatte.NewMatcherArg(arg.Context(), arg.Explain(), doc))
	if err != nil {
		return failure(err)
	}

	if ok {
		return success()
	}

	return mismatch(m.Name() + "() claims did not match")
}

// Describe implements vlatte.SelfDescriptive.
func (m *jwtMatcher) Describe() vlatte.Descriptor {
	if m.keySet == nil {
		return vlatte.DescribeSingleArg("jwt", describe(m.Matcher))
	}

	return vlatte.DescribeMultiArgs("signedJWT", m.Keys, describe(m.Matcher))
}

// bearerToken returns the token of text, after the scheme of an Authorization header value, if any.
func bearerToken(text string) string {
	text = strings.TrimSpace(text)
	if _, token, ok := strings.Cut(text, " "); ok {
		return strings.TrimSpace(token)
	}

	return text
}
