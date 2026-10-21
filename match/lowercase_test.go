package match

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/caffeine-projects/vlatte"
)

func TestToLowerCase(t *testing.T) {
	ok, err := ToLower(StrictEqual("test")).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "TeST"))

	assert.Nil(t, err)
	assert.True(t, ok)
}
