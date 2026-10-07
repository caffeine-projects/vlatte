package match

import (
	json "encoding/json/v2"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestToEqualJSON(t *testing.T) {
	testCases := []struct {
		a        any
		b        any
		expected bool
	}{
		{
			map[string]any{"ok": true, "name": "dev"},
			map[string]any{"ok": true, "name": "dev"},
			true,
		},
		{
			map[string]any{"ok": true, "name": "dev"},
			map[string]any{"nok": true, "name": "dev"},
			false,
		},

		{
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			true,
		},
		{
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			`{"name":"no-one","job":{"title":"qa"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			false,
		},
		{
			`{"name":"no-one","job":{"title":"dev","level": 3},"tags":["hi","hello","tchau","bye"],"address":{"number":100,"street":"nowhere","city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			`{"name":"no-one","job":{"level": 3,"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			true,
		},
		{
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.544Z"}`,
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			false,
		},
		{
			`{"name":"no-one","job":{"title":"dev"},"tags":["hello","tchau","bye","hi"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			`{"name":"no-one","job":{"title":"dev"},"tags":["hi","hello","tchau","bye"],"address":{"street":"nowhere","number":100,"city":{"name":"Berlin"}},"active":true,"balance":1250.75,"timestamp":1679084156116,"last_update":"2023-03-17T20:16:21.584Z"}`,
			false,
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.FormatInt(int64(i), 10), func(t *testing.T) {
			ok, err := EqualJSON(tc.a).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.b))

			require.NoError(t, err)
			require.Equal(t, tc.expected, ok)
		})
	}
}

func TestEqualJSON_PanicsOnWhatIsntJSON(t *testing.T) {
	// An expectation that isn't JSON is a mistake in code; mock files catch it when they load.
	// json/v2 words its errors at random, with "cannot" or "unable to", so only the error's type is checked.
	err, ok := panicOf(func() { EQJ(make(chan struct{})) }).(error)
	require.True(t, ok)
	require.ErrorContains(t, err, "match: eqj: json: ")

	_, isSemantic := errors.AsType[*json.SemanticError](err)
	require.True(t, isSemantic)

	require.Panics(t, func() { EqualJSON(`{"a": `) })
}

func TestEqualJSON_ValuesThatArentJSON(t *testing.T) {
	// A request value that isn't JSON doesn't match, and isn't an error.
	for _, v := range []any{"{", make(chan int), "x"} {
		ok, err := EqualJSON(`{"a": 1}`).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), v))
		require.NoError(t, err)
		require.False(t, ok)
	}
}

func TestEqualJSON_NumbersByValue(t *testing.T) {
	// A file's 1.0 decodes to the integer 1, and a body's 1.0 is the same JSON number: they must match.
	for _, body := range []string{`{"n": 1.0}`, `{"n": 1}`, `{"n": 1e0}`} {
		ok, err := EqualJSON(map[string]any{"n": int64(1)}).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), body))
		require.NoError(t, err)
		require.True(t, ok, body)
	}

	ok, err := EqualJSON(`{"n": "1"}`).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), `{"n": 1}`))
	require.NoError(t, err)
	require.False(t, ok, "JSON text is not a JSON number")
}

func TestEqualJSON_ParsesIncomingValuesLeniently(t *testing.T) {
	// A mock stands in for a real server, so it must not reject request bodies a lenient server accepts.
	testCases := []struct {
		name     string
		expected string
		value    string
	}{
		{"duplicate keys, last one wins", `{"name":"dev"}`, `{"name":"qa","name":"dev"}`},
		{"invalid UTF-8 becomes U+FFFD", `{"name":"dev�"}`, "{\"name\":\"dev\xff\"}"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := EqualJSON(tc.expected).Match(vlatte.NewMatcherArg(t.Context(), vlatte.NewExplanation(), tc.value))

			require.NoError(t, err)
			require.True(t, ok)
		})
	}
}

// panicOf returns the value fn panics with, or nil.
func panicOf(fn func()) (v any) {
	defer func() { v = recover() }()

	fn()

	return nil
}
