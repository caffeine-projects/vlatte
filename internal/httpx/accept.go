package httpx

import (
	"mime"
	"regexp"
	"strconv"
	"strings"
)

// _qvalue is a weight as RFC 9110 writes one: 0 to 1, with up to three decimals.
var _qvalue = regexp.MustCompile(`^(0(\.[0-9]{0,3})?|1(\.0{0,3})?)$`)

// AcceptRange is a media range of an Accept header, such as application/json, text/* or */*, and its weight.
type AcceptRange struct {
	MediaType string
	Q         float64
}

// Preference is how much the ranges of Accept headers want a media type: Q is the weight of the range that matches
// it, Specificity tells how specifically that range matches, as [AcceptRange.Match] does, and Index is where the range
// sits among the ranges. A media type no range matches has a zero Preference.
type Preference struct {
	Q           float64
	Specificity int
	Index       int
}

// ParseAccept parses the media ranges of Accept headers, in the order they come, leaving out those it can't parse and
// those whose weight isn't one RFC 9110 allows, from 0 to 1.
func ParseAccept(header []string) []AcceptRange {
	var ranges []AcceptRange

	for _, h := range header {
		for item := range strings.SplitSeq(h, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil {
				continue
			}

			q := 1.0
			if v, ok := params["q"]; ok {
				if !_qvalue.MatchString(v) {
					continue
				}

				q, _ = strconv.ParseFloat(v, 64)
			}

			ranges = append(ranges, AcceptRange{MediaType: mediaType, Q: q})
		}
	}

	return ranges
}

// Match tells how specifically r matches mediaType: 3 by name, 2 by type, as text/*, 1 as */*, 0 not.
func (r AcceptRange) Match(mediaType string) int {
	switch r.MediaType {
	case mediaType:
		return 3
	case "*/*":
		return 1
	}

	prefix, ok := strings.CutSuffix(r.MediaType, "*")
	if ok && strings.HasSuffix(prefix, "/") && strings.HasPrefix(mediaType, prefix) {
		return 2
	}

	return 0
}

// Prefer returns how much ranges want a media type with one of names: the most specific range that matches a name
// decides, as RFC 9110 reads Accept, the heaviest of those when several are as specific, and the first of those when
// they weigh the same.
func Prefer(ranges []AcceptRange, names ...string) Preference {
	var best Preference

	for i, rng := range ranges {
		s := 0
		for _, name := range names {
			s = max(s, rng.Match(name))
		}

		if s > best.Specificity || s > 0 && s == best.Specificity && rng.Q > best.Q {
			best = Preference{Q: rng.Q, Specificity: s, Index: i}
		}
	}

	return best
}
