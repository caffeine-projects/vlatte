package filter

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/require"
)

func TestIsPresent(t *testing.T) {
	resYes, _ := Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),"test"))
	resNo, _ := Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),""))
	require.True(t, resYes)
	require.False(t, resNo)

	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),1))
	require.True(t, resYes)

	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),0))
	require.True(t, resYes)

	p := "test"
	resYes, _ = Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),&p))
	resNo, _ = Present().Match(vlatte.NewFilterArg(t.Context(), explain.New(),nil))
	require.True(t, resYes)
	require.False(t, resNo)
}
