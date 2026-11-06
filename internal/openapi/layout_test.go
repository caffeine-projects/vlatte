package openapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/openapi"
)

func TestPerMock_WritesEachMockToAFileNamedAfterIt(t *testing.T) {
	// The file name is the mock's ID: the name a user sees in the folder is the one the journal and the admin API
	// show.
	b := openapi.Mock{ID: "b-get-200"}
	a := openapi.Mock{ID: "a-get-200"}

	require.Equal(t, []openapi.File{
		{Name: "a-get-200.mock.yaml", Mocks: []openapi.Mock{a}},
		{Name: "b-get-200.mock.yaml", Mocks: []openapi.Mock{b}},
	}, openapi.PerMock([]openapi.Mock{b, a}))
}
