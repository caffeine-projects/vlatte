package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/require"
)

func TestIsPresent(t *testing.T) {
	resYes, _ := Present().Match(vlatte.NewFilterArg(t.Context(),"test"))
	resNo, _ := Present().Match(vlatte.NewFilterArg(t.Context(),""))
	require.True(t, resYes)
	require.False(t, resNo)

	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(),1))
	require.True(t, resYes)

	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(),0))
	require.True(t, resYes)

	p := "test"
	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(),&p))
	resNo, _ = Present().Match(vlatte.NewFilterArg(t.Context(),nil))
	require.True(t, resYes)
	require.False(t, resNo)
}
