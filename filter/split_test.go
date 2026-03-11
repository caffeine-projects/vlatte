package filter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplit(t *testing.T) {
	v := "test, testing, tested"
	ok, err := Split(",", Each(Trim(HasPrefix("test")))).Match(v)

	require.NoError(t, err)
	require.True(t, ok)
}
