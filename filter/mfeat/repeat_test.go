package mfeat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepeat(t *testing.T) {
	rep := Repeat(2)
	stateful, _ := rep.(interface{ SetState() error })

	ok, _, err := rep.Match(nil)
	assert.NoError(t, stateful.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, _, err = rep.Match(nil)
	assert.NoError(t, stateful.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, _, err = rep.Match(nil)
	assert.NoError(t, stateful.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)

	ok, _, err = rep.Match(nil)
	assert.NoError(t, stateful.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)
}
