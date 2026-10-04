package filter

import (
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
	"github.com/stretchr/testify/assert"
)

func TestXor(t *testing.T) {
	m := XOR(Contain("dev"), Contain("test"))

	t.Run("should return true when left condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "dev"))
		assert.Nil(t, err)
		assert.True(t, ok)
	})

	t.Run("should return true right condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "test"))
		assert.Nil(t, err)
		assert.True(t, ok)
	})

	t.Run("should return false both condition matches", func(t *testing.T) {
		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "dev-test"))
		assert.Nil(t, err)
		assert.False(t, ok)
	})
}

func TestXorError(t *testing.T) {
	t.Run("should return error from right matcher", func(t *testing.T) {
		m := XOR(Contain("dev"), Function(func(_ any) (bool, error) {
			return false, fmt.Errorf("fail")
		}))

		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "dev"))

		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("should return error from left matcher", func(t *testing.T) {
		m := XOR(
			Function(func(_ any) (bool, error) {
				return false, fmt.Errorf("fail")
			}),
			Contain("dev"))

		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "dev"))

		assert.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("should return error when both matchers fails", func(t *testing.T) {
		m := XOR(
			Function(func(_ any) (bool, error) {
				return false, fmt.Errorf("fail first")
			}),
			Function(func(_ any) (bool, error) {
				return false, fmt.Errorf("fail second")
			}))

		ok, err := m.Match(vlatte.NewFilterArg(t.Context(), explain.New(), "nothing"))

		assert.Error(t, err)
		assert.False(t, ok)
	})
}
