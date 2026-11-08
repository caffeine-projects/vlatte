package pathtemplate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi/pathtemplate"
)

func TestTemplated_CountsTheSegmentsThatHoldAParameter(t *testing.T) {
	// A segment of two parameters is one templated segment: /files/{name}.{ext} is as specific as /files/{id}.
	tests := []struct {
		path string
		want int
	}{
		{path: "/pets/mine", want: 0},
		{path: "/pets/{id}", want: 1},
		{path: "/files/{name}.{ext}", want: 1},
		{path: "/owners/{owner}/pets/{id}", want: 2},
		{path: "/v1/{tenant}/pets/{id}/", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			require.Equal(t, tt.want, pathtemplate.Templated(tt.path))
		})
	}
}
