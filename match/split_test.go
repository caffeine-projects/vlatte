package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestSplit(t *testing.T) {
	v := "test, testing, tested"
	ok, err := Split(",", Each(Trim(HasPrefix("test")))).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))

	require.NoError(t, err)
	require.True(t, ok)
}
