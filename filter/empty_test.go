package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestIsEmpty(t *testing.T) {
	resYes, err := Empty().Match(vlatte.NewFilterArg(t.Context(),""))
	assert.Nil(t, err)
	resNo, err := Empty().Match(vlatte.NewFilterArg(t.Context(),"test"))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewFilterArg(t.Context(),[]string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewFilterArg(t.Context(),[]string{"test"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)

	resYes, err = Empty().Match(vlatte.NewFilterArg(t.Context(),map[string]string{}))
	assert.Nil(t, err)
	resNo, err = Empty().Match(vlatte.NewFilterArg(t.Context(),map[string]string{"k": "v"}))
	assert.Nil(t, err)
	assert.True(t, resYes)
	assert.False(t, resNo)
}
