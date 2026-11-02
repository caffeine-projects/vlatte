package match

import (
	"fmt"
	"slices"

	"github.com/caffeine-projects/vlatte"
)

// Accept matches the values of an Accept header, a list of them or one string, when mediaType is the one of offers,
// the media types an operation answers with, that the client prefers: the offer the heaviest range takes, the first
// of offers on a tie. A range with q=0 refuses its media types. A request without Accept, or with only ranges that
// don't parse, takes the first offer. Media types ignore case and parameters. Without offers, mediaType is the only
// one; Accept panics when offers doesn't hold mediaType.
//
// Give each representation of an operation its own mock with an Accept of the same offers: the client's q-values,
// not the order of the mocks, decide which one answers. [NotAcceptable] matches the requests none of them take.
func Accept(mediaType string, offers ...string) vlatte.Matcher {
	m := &acceptMatcher{mediaType: normalizeMediaType(mediaType), offers: normalizeMediaTypes(offers)}
	if len(m.offers) == 0 {
		m.offers = []string{m.mediaType}
	}

	if !slices.Contains(m.offers, m.mediaType) {
		panic(fmt.Errorf("match: accept: expected %q among the offers %q", m.mediaType, m.offers))
	}

	return m
}

// NotAcceptable matches the values of an Accept header, as [Accept] reads them, that take none of offers: every
// range that matches an offer has q=0, or none does. A request without Accept, or with only ranges that don't parse,
// takes any offer, so it doesn't match. Without offers, NotAcceptable matches every Accept that parses.
func NotAcceptable(offers ...string) vlatte.Matcher {
	return &notAcceptableMatcher{offers: normalizeMediaTypes(offers)}
}
