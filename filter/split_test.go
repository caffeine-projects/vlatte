package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestSplit(t *testing.T) {
	v := "test, testing, tested"
	ok, err := Split(",", Each(Trim(HasPrefix("test")))).Match(vlatte.NewFilterArg(t.Context(), explain.New(), v))

	require.NoError(t, err)
	require.True(t, ok)
}
