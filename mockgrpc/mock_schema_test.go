package mockgrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestCodeOf_ReadsTheNamesGRPCWritesAndDescriptionsWrite(t *testing.T) {
	// A description writes a code by its name: every name loads back as its code, as do the Go names and numbers.
	for c := range codes.Code(17) {
		for _, give := range []any{codeName(c), c.String(), int64(c)} {
			got, err := codeOf(config.Value{Key: "code", Any: give})
			require.NoError(t, err, "%v", give)
			require.Equal(t, c, got, "%v", give)
		}
	}

	require.Equal(t, "OK", codeName(codes.OK))
	require.Equal(t, "DEADLINE_EXCEEDED", codeName(codes.DeadlineExceeded))

	_, err := codeOf(config.Value{Key: "code", Any: int64(17)})
	require.Error(t, err)
}
