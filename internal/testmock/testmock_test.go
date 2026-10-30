package testmock_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/testmock"
)

func TestFakeT_RecordsTestingCallsWithTheirArguments(t *testing.T) {
	ft := testmock.NewFakeT()
	ft.Helper()
	ft.Logf("loaded %s in %dms", "tea", 3)
	ft.Error("unexpected status: ", 500)
	ft.Errorf("expected %d requests, got %d", 2, 1)

	ft.AssertCalled(t, "Helper")
	ft.AssertCalled(t, "Logf", "loaded %s in %dms", []any{"tea", 3})
	ft.AssertCalled(t, "Error", []any{"unexpected status: ", 500})
	ft.AssertCalled(t, "Errorf", "expected %d requests, got %d", []any{2, 1})
	for _, method := range []string{"Helper", "Logf", "Error", "Errorf"} {
		ft.AssertNumberOfCalls(t, method, 1)
	}
}

func TestFakeT_ErrorsReturnsOnlyFormattedErrorfMessagesInOrder(t *testing.T) {
	ft := testmock.NewFakeT()
	require.Len(t, ft.Errors(), 0)
	ft.Logf("loaded %s", "tea")
	ft.Error("an unformatted error")
	require.Len(t, ft.Errors(), 0)

	ft.Errorf("expected %d %s, got %q", 2, "cups", "none")
	ft.Helper()
	ft.Errorf("operation failed")
	ft.Errorf("progress: %d%%", 25)

	require.Equal(t, []string{
		`expected 2 cups, got "none"`,
		"operation failed",
		"progress: 25%",
	}, ft.Errors())
}

func TestFakeT_ErrorsReturnsAnIndependentSnapshot(t *testing.T) {
	ft := testmock.NewFakeT()
	ft.Errorf("first failure: %d", 1)
	first := ft.Errors()
	require.Equal(t, []string{"first failure: 1"}, first)

	ft.Errorf("second failure: %d", 2)
	require.Equal(t, []string{"first failure: 1"}, first)
	first[0] = "changed by the caller"
	require.Equal(t, []string{"first failure: 1", "second failure: 2"}, ft.Errors())
}

func TestFakeT_CleanupDoesNotRunTheCallback(t *testing.T) {
	ft := testmock.NewFakeT()
	ran := false
	ft.Cleanup(func() { ran = true })
	require.False(t, ran)
}
