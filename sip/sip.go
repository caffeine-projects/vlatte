package sip

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
)

func Times[M vlatte.Bean](t vlatte.TestingT, taste vlatte.Taste[M], expected int64) bool {
	t.Helper()

	hits := taste.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hits.\nGot %d", expected, hits)

	return false
}

func NotCalled[M vlatte.Bean](t vlatte.TestingT, taste vlatte.Taste[M]) bool {
	t.Helper()

	called := taste.ListCalled()
	if len(called) == 0 {
		return true
	}

	b := strings.Builder{}
	size := len(called)

	for _, p := range called {
		b.WriteString("  Mock [")
		b.WriteString(p.GetID())
		b.WriteString("] ")
		b.WriteString(p.GetName())
		b.WriteString("\n")
	}

	t.Errorf("\n%d Mocks were called at least once when none should be.\nCalled:\n%s", size, b.String())

	return false
}
