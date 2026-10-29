package vlatte

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain fails the package when a goroutine outlives its tests. It ignores none: an idle keep-alive connection is
// one too, and ends when the test closes its server or its client's idle connections.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
