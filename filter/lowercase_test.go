package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/assert"
)

func TestToLowerCase(t *testing.T) {
	ok, err := ToLower(StrictEqual("test")).Match(vlatte.NewFilterArg(t.Context(), explain.New(), "TeST"))

	assert.Nil(t, err)
	assert.True(t, ok)
}
