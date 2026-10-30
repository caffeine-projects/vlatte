package anyconv_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

func TestStringify_DiagnosticsKeepIntegerValuesAndOmitRequestSecrets(t *testing.T) {
	require.Equal(t, "-2147483648", anyconv.Stringify(int32(-2147483648)))
	req, err := http.NewRequest(http.MethodGet, "http://example.org/?token=secret", nil)
	require.NoError(t, err)
	require.Equal(t, "*http.Request", anyconv.Stringify(req))
}
