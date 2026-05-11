package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/assert"
)

func TestIsEmpty(t *testing.T) {
	resYes, err := Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),""))
	assert.Nil(t, err)
	resNo, err := Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),"test"))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),[]string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),[]string{"test"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),map[string]string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewFilterArg(t.Context(), explain.New(),map[string]string{"k": "v"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)
}
