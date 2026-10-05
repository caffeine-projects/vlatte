package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestToLowerCase(t *testing.T) {
	ok, err := ToLower(StrictEqual("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "TeST"))

	assert.Nil(t, err)
	assert.True(t, ok)
}
