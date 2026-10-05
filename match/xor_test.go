package match

import (
	"errors"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/stretchr/testify/assert"
)

func TestXor(t *testing.T) {
	m := XOR(Contain("dev"), Contain("test"))

	t.Run("should return true when left condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "dev"))
		assert.Nil(t, err)
		assert.True(t, ok)
	})

	t.Run("should return true right condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "test"))
		assert.Nil(t, err)
		assert.True(t, ok)
	})

	t.Run("should return false both condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "dev-test"))
		assert.Nil(t, err)
		assert.False(t, ok)
	})
}

func TestXorError(t *testing.T) {
	t.Run("should return error from right matcher", func(t *testing.T) {
		m := XOR(Contain("dev"), Function(func(_ any) (bool, error) {
			return false, errors.New("fail")
		}))

		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "dev"))

		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("should return error from left matcher", func(t *testing.T) {
		m := XOR(
			Function(func(_ any) (bool, error) {
				return false, errors.New("fail")
			}),
			Contain("dev"))

		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "dev"))

		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("should return error when both matchers fails", func(t *testing.T) {
		m := XOR(
			Function(func(_ any) (bool, error) {
				return false, errors.New("fail first")
			}),
			Function(func(_ any) (bool, error) {
				return false, errors.New("fail second")
			}))

		ok, err := m.Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), "nothing"))

		assert.Error(t, err)
		assert.False(t, ok)
	})
}
