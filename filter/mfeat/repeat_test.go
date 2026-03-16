package mfeat

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/shelf/explain"
	"github.com/stretchr/testify/assert"
)

func TestRepeat(t *testing.T) {
	rep := Repeat(2)

	ok, err := rep.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.True(t, ok)

	ok, err = rep.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)

	ok, err = rep.Match(vlatte.NewFilterArg(t.Context(), explain.New(), nil))
	assert.NoError(t, rep.SetState())
	assert.NoError(t, err)
	assert.False(t, ok)
}
