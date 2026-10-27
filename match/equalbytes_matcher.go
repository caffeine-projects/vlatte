package match

import (
	"encoding/base64"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var (
	_ vlatte.Matcher             = (*equalBytesMatcher)(nil)
	_ descriptor.SelfDescriptive = (*equalBytesMatcher)(nil)
)

// equalBytesMatcher passes when the value is text of the expected bytes.
type equalBytesMatcher struct {
	Expected []byte
}

// Name implements vlatte.Matcher.
func (m *equalBytesMatcher) Name() string { return "EqualBytes" }

// Match implements vlatte.Matcher.
func (m *equalBytesMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	txt, ok := value.Text(arg.Value())
	if !ok {
		return mismatch("EqualBytes() Value is not text")
	}

	if txt == string(m.Expected) {
		return success()
	}

	// In base64, as the bytes may not be text.
	return mismatch("EqualBytes(" + base64.StdEncoding.EncodeToString(m.Expected) + ") Got: " +
		base64.StdEncoding.EncodeToString([]byte(txt)))
}

// Describe implements descriptor.SelfDescriptive.
func (m *equalBytesMatcher) Describe() descriptor.Descriptor {
	return descriptor.SingleArg("equalbytes", base64.StdEncoding.EncodeToString(m.Expected))
}
