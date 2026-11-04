package match

import (
	"mime"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/httpx"
)

var (
	_ vlatte.Matcher         = (*acceptMatcher)(nil)
	_ vlatte.SelfDescriptive = (*acceptMatcher)(nil)
	_ vlatte.Matcher         = (*notAcceptableMatcher)(nil)
	_ vlatte.SelfDescriptive = (*notAcceptableMatcher)(nil)
)

// acceptMatcher passes when the client prefers mediaType among offers.
type acceptMatcher struct {
	mediaType string
	offers    []string
}

// Name implements vlatte.Matcher.
func (m *acceptMatcher) Name() string { return "Accept" }

// Match implements vlatte.Matcher.
func (m *acceptMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	best := 0
	if ranges := httpx.ParseAccept(acceptValues(arg.Value())); len(ranges) > 0 {
		best = preferred(ranges, m.offers)
	}

	if best >= 0 && m.offers[best] == m.mediaType {
		return success()
	}

	return mismatch(arg, "Accept("+m.mediaType+") the client prefers another offer")
}

// Describe implements vlatte.SelfDescriptive.
func (m *acceptMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeMultiArgs("accept", m.mediaType, anys(m.offers))
}

// notAcceptableMatcher passes when an Accept header takes none of offers.
type notAcceptableMatcher struct {
	offers []string
}

// Name implements vlatte.Matcher.
func (m *notAcceptableMatcher) Name() string { return "NotAcceptable" }

// Match implements vlatte.Matcher.
func (m *notAcceptableMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	if ranges := httpx.ParseAccept(acceptValues(arg.Value())); len(ranges) > 0 && preferred(ranges, m.offers) < 0 {
		return success()
	}

	return mismatch(arg, "NotAcceptable() the client takes an offer")
}

// Describe implements vlatte.SelfDescriptive.
func (m *notAcceptableMatcher) Describe() vlatte.Descriptor {
	return vlatte.DescribeSingleArg("notacceptable", anys(m.offers))
}

// preferred returns the index of the offer ranges weigh highest, the first on a tie, or -1 when they take none.
func preferred(ranges []httpx.AcceptRange, offers []string) int {
	best, bestQ := -1, 0.0

	for i, offer := range offers {
		if q := httpx.Prefer(ranges, offer).Q; q > bestQ {
			best, bestQ = i, q
		}
	}

	return best
}

// acceptValues returns the values of an Accept header that v holds: all of them, or the first, as text.
func acceptValues(v any) []string {
	switch v := v.(type) {
	case []string:
		return v
	case string:
		return []string{v}
	default:
		return nil
	}
}

// normalizeMediaTypes returns mediaTypes, each as normalizeMediaType returns it.
func normalizeMediaTypes(mediaTypes []string) []string {
	out := make([]string, len(mediaTypes))
	for i, mt := range mediaTypes {
		out[i] = normalizeMediaType(mt)
	}

	return out
}

// normalizeMediaType returns mediaType in lowercase, without its parameters.
func normalizeMediaType(mediaType string) string {
	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		return mt
	}

	return strings.ToLower(strings.TrimSpace(mediaType))
}
