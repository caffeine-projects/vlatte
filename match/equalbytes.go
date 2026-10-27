package match

import (
	"bytes"

	"github.com/caffeine-projects/vlatte"
)

// EqualBytes matches text, a string or a byte slice, whose bytes are expected, exactly: unlike [Equal], it reads no
// text as a number or a bool. It matches a body that isn't UTF-8 text, such as an image, and describes the bytes in
// standard base64, as mock files write them: [equalbytes, "//4A"].
func EqualBytes(expected []byte) vlatte.Matcher {
	return &equalBytesMatcher{Expected: bytes.Clone(expected)}
}
