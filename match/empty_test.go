package match

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestIsEmpty(t *testing.T) {
	resYes, err := Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), ""))
	assert.Nil(t, err)
	resNo, err := Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), []string{"test"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), map[string]string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), map[string]string{"k": "v"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)
}
