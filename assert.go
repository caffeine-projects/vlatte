package vlatte

import "strings"

// Times asserts that the inspector recorded exactly expected hits.
func Times[M Mock](t TestingT, inspector Inspector[M], expected int64) bool {
	t.Helper()

	hits := inspector.Hits()

	if hits == expected {
		return true
	}

	t.Errorf("\nExpected %d matched request hits.\nGot %d", expected, hits)

	return false
}

// NotCalled asserts that none of the mocks tracked by inspector were called.
func NotCalled[M Mock](t TestingT, inspector Inspector[M]) bool {
	t.Helper()

	called := inspector.ListCalled()
	if len(called) == 0 {
		return true
	}

	b := strings.Builder{}
	size := len(called)

	for _, p := range called {
		b.WriteString("  Mock [")
		b.WriteString(p.ID())
		b.WriteString("] ")
		b.WriteString(p.Name())
		b.WriteString("\n")
	}

	t.Errorf("\n%d Mocks were called at least once when none should be.\nCalled:\n%s", size, b.String())

	return false
}
