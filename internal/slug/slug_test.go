package slug_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/slug"
)

func TestMake_KeepsOnlyWhatEveryFileSystemAccepts(t *testing.T) {
	// A slug becomes part of a file name: it must open on every system, and never hide the file or climb out of
	// its folder.
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "safe text", in: "pets.get_200-v2", max: 80, want: "pets.get_200-v2"},
		{name: "a run of other runes", in: "GET /items?page=2", max: 80, want: "GET-items-page-2"},
		{name: "a path that climbs", in: "../../etc/passwd", max: 80, want: "etc-passwd"},
		{name: "a hidden name", in: ".hidden", max: 80, want: "hidden"},
		{name: "no letter", in: " ?! ", max: 80, want: ""},
		{name: "letters past ASCII", in: "café", max: 80, want: "caf"},
		{name: "a media type", in: "vnd.api+json", max: 80, want: "vnd.api-json"},
		{name: "longer than max", in: "abcdefgh", max: 4, want: "abcd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, slug.Make(tt.in, tt.max))
		})
	}
}
