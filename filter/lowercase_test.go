package filter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToLowerCase(t *testing.T) {
	ok, _, err := ToLower(StrictEqual("test")).Match("TeST")

	assert.Nil(t, err)
	assert.True(t, ok)
}
