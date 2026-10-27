package match

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

func TestEqualBytes(t *testing.T) {
	// A body that isn't UTF-8, such as an image, matches by its bytes, exactly: unlike Equal, no text reads as a
	// number or a bool.
	tests := []struct {
		name     string
		expected []byte
		value    any
		want     bool
	}{
		{"the same bytes", []byte{0xff, 0xfe, 0x00}, []byte{0xff, 0xfe, 0x00}, true},
		{"text of the same bytes", []byte{0xff, 0xfe, 0x00}, "\xff\xfe\x00", true},
		{"other bytes", []byte{0xff, 0xfe, 0x00}, []byte{0xff, 0xfe}, false},
		{"text that reads as the same number", []byte("10"), "10.0", false},
		{"a number isn't text", []byte("1"), 1, false},
		{"nil isn't empty text", []byte{}, nil, false},
		{"no bytes match empty text", nil, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := EqualBytes(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))
			require.NoError(t, err)
			require.Equal(t, tc.want, ok)
		})
	}
}

func TestEqualBytes_DescribesItsBytesInBase64(t *testing.T) {
	// JSON and YAML hold text as UTF-8: the bytes go in standard base64, so a mock's description encodes, and
	// [equalbytes, ...] reads it back.
	desc, ok := descriptor.IsSelfDescriptive(EqualBytes([]byte{0xff, 0xfe, 0x00}))
	require.True(t, ok)

	b, err := desc.MarshalJSON()
	require.NoError(t, err)
	require.JSONEq(t, `["equalbytes", "//4A"]`, string(b))
}
