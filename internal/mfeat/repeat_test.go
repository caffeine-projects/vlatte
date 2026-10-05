package mfeat

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestRepeat(t *testing.T) {
	rep := Repeat(2)

	ok, err := rep.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)

	ok, err = rep.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)
}
