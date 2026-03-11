package mfeat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepeat(t *testing.T) {
	rep := Repeat(2)

	ok, err := rep.Match(nil)
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(nil)
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(nil)
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)

	ok, err = rep.Match(nil)
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)
}
