package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestToLowerCase(t *testing.T) {
	ok, err := ToLower(StrictEqual("test")).Match(vlatte.NewFilterArg(t.Context(), "TeST"))

	assert.Nil(t, err)
	assert.True(t, ok)
}
