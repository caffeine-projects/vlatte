package vlatte_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
)

func TestNotCalled_NamesOnlyTheMocksThatWereCalled(t *testing.T) {
	called := newFakeMockWith("called", 0)
	pending := newFakeMockWith("pending", 0)
	scope := vlatte.NewScope([]*fakeMock{called, pending}, vlatte.NewStore[*fakeMock]())
	ft := testmock.NewFakeT()

	require.True(t, vlatte.NotCalled(ft, scope))
	require.Empty(t, ft.Errors())

	called.hit()
	require.False(t, vlatte.NotCalled(ft, scope))
	require.Equal(t, "\n1 Mocks were called at least once when none should be.\nCalled:\n  Mock [called] called\n", errorText(t, ft))
}
