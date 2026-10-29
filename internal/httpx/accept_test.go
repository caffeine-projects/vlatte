package httpx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAccept_KeepsTheOrderAndDropsWhatDoesNotParse(t *testing.T) {
	// A server that breaks ties by the order of Accept reads the ranges as the client listed them; a range or weight
	// that doesn't parse counts for nothing, so a typo in one doesn't refuse the request.
	ranges := ParseAccept([]string{"application/json, text/plain;q=0.5", ";q=1, text/markdown;q=high, */*;q=0.1"})

	require.Equal(t, []AcceptRange{
		{MediaType: "application/json", Q: 1},
		{MediaType: "text/plain", Q: 0.5},
		{MediaType: "*/*", Q: 0.1},
	}, ranges)
}

func TestPrefer_TheMostSpecificRangeDecides(t *testing.T) {
	// RFC 9110: the most specific range that matches a media type gives its weight, so text/plain;q=0 refuses text/plain
	// whatever */* allows. Among ranges as specific, the heaviest decides, and among those, the first listed.
	tests := []struct {
		name   string
		accept string
		names  []string
		want   Preference
	}{
		{name: "none", accept: "application/xml", names: []string{"text/plain"}, want: Preference{}},
		{name: "by name", accept: "*/*, text/plain;q=0.4", names: []string{"text/plain"}, want: Preference{Q: 0.4, Specificity: 3, Index: 1}},
		{name: "by type", accept: "*/*;q=0.2, text/*;q=0.3", names: []string{"text/plain"}, want: Preference{Q: 0.3, Specificity: 2, Index: 1}},
		{name: "any", accept: "application/json, */*;q=0.8", names: []string{"text/plain"}, want: Preference{Q: 0.8, Specificity: 1, Index: 1}},
		{name: "refused by name", accept: "text/plain;q=0, */*", names: []string{"text/plain"}, want: Preference{Specificity: 3}},
		{name: "heaviest", accept: "text/x;q=0.2, text/y;q=0.9", names: []string{"text/x", "text/y"}, want: Preference{Q: 0.9, Specificity: 3, Index: 1}},
		{name: "first", accept: "text/x, text/y", names: []string{"text/y", "text/x"}, want: Preference{Q: 1, Specificity: 3, Index: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, Prefer(ParseAccept([]string{tt.accept}), tt.names...))
		})
	}
}
