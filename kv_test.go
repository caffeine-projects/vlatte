package vlatte_test

import (
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/kvtest"
)

func TestMemoryKV_KeepsTheKVContract(t *testing.T) {
	kvtest.Run(t, func(*testing.T) vlatte.KV { return vlatte.NewMemoryKV() })
}
