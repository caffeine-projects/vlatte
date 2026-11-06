package payments_test

// The docs home page shows the imports and TestCharge, from here to the end of the region.
// #region example
import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/examples/02-http-payments/payments"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestCharge(t *testing.T) {
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	paid := m.MustMock(mockhttp.Postf("/payments").
		Header("Authorization", match.HasPrefix("Bearer ")).
		Body(match.JSONPath("$.amount", match.GreaterThan(0))).
		Reply(mockhttp.Created().
			JSON(map[string]any{"id": "pay_1"})))

	client := payments.NewClient(m.URL(), "token")
	id, err := client.Charge(t.Context(), 42)

	require.NoError(t, err)
	require.Equal(t, "pay_1", id)
	paid.AssertCalled(t)
}

// #endregion example

func TestMockFileAnswersTheChargeAsTheGoMockDoes(t *testing.T) {
	// The home page shows this mock file, and the CLI serving it from the folder above, next to TestCharge. The file stays
	// out of this package's testdata/vlatte/_mocks, which every server of these tests loads on start: there, it would
	// answer TestCharge in place of its own mock.
	m := mockhttp.MustNew()
	m.MustStart()
	t.Cleanup(m.Close)

	paid := m.MustMock(mockhttp.FromFile("../testdata/vlatte/_mocks/payments_mock.yaml"))

	id, err := payments.NewClient(m.URL(), "token").Charge(t.Context(), 42)

	require.NoError(t, err)
	require.Equal(t, "pay_1", id)
	paid.AssertCalled(t)
}
