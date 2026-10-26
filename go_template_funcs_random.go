package vlatte

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

var (
	// errBounds says a random function got a max not above its min.
	errBounds = errors.New("max must be above min")

	// errFloatSpan says randomFloat got bounds too far apart for a float64.
	errFloatSpan = errors.New("max - min overflows float64")
)

// _alphabets are the kinds of randomString, by name.
var _alphabets = map[string]string{
	"alphanumeric":        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
	"alphabetic":          "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"numeric":             "0123456789",
	"alphanumericSymbols": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&()*+,-./:;<=>?@[]^_{|}~",
	"hex":                 "0123456789abcdef",
}

// randomString returns length random characters of kind: alphanumeric, alphabetic, numeric, alphanumericSymbols
// or hex. It is not for secrets.
func randomString(kind string, length int) (string, error) {
	alphabet, ok := _alphabets[kind]
	if !ok {
		return "", fmt.Errorf("unknown kind %q", kind)
	}

	if length < 0 {
		return "", fmt.Errorf("negative length %d", length)
	}

	var b strings.Builder

	b.Grow(length)

	for range length {
		b.WriteByte(alphabet[rand.IntN(len(alphabet))])
	}

	return b.String(), nil
}

// randomInt returns a random int in [minimum, maximum).
func randomInt(minimum, maximum int) (int, error) {
	if maximum <= minimum {
		return 0, errBounds
	}

	// The span is an uint64, so one wider than an int, such as of the whole range of int, doesn't overflow.
	return int(uint64(minimum) + rand.Uint64N(uint64(maximum)-uint64(minimum))), nil
}

// randomFloat returns a random float64 in [minimum, maximum).
func randomFloat(minimum, maximum float64) (float64, error) {
	if maximum <= minimum {
		return 0, errBounds
	}

	span := maximum - minimum
	if math.IsInf(span, 0) {
		return 0, errFloatSpan
	}

	return minimum + rand.Float64()*span, nil
}
