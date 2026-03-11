package filter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToLowerCase(t *testing.T) {
	ok, err := ToLower(StrictEqual("test")).Match("TeST")

	assert.Nil(t, err)
	assert.True(t, ok)
}
