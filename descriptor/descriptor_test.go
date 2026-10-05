package descriptor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDescriptor_MarshalJSON(t *testing.T) {
	type testCase struct {
		key          string
		props        any
		expectedJSON string
	}

	testCases := []testCase{
		{"simple slice", []any{"a", "b", false, 10}, `["simple slice","a","b",false,10]`},
		{"map", map[string]any{"a": "b", "c": true, "d": 100, "e": 1500.25}, `{"a":"b","c":true,"d":100,"e":1500.25}`},
		{"map with slice", map[string]any{"a": "b", "c": []any{"d", "e", false, 100.25}}, `{"a":"b","c":["d","e",false,100.25]}`},
		{"array with map", []any{"a", "b", map[string]any{"c": "d", "e": []int{1, 2, 3}}}, `["array with map","a","b",{"c":"d","e":[1,2,3]}]`},
		{"slice + inner slice", []any{"a", "b", []any{"c", "d"}}, `["slice + inner slice","a","b",["c","d"]]`},
		{"arrays", []any{"a", "b", []any{"c", "d"}, []any{"e", "f"}}, `["arrays","a","b",["c","d"],["e","f"]]`},
		{"inner descriptor", []any{"a", "b", New("inner", []any{"c", "d"})}, `["inner descriptor","a","b",["inner","c","d"]]`},
		{"only key", nil, `["only key"]`},
		{"descriptor", New("key", []any{"a", "b", false, 10}), `["key","a","b",false,10]`},
	}

	for _, tc := range testCases {
		t.Run(tc.key, func(t *testing.T) {
			d := &Descriptor{Key: tc.key, Props: tc.props}
			err := d.Validate()
			require.NoError(t, err)

			b, err := d.MarshalJSON()
			require.NoError(t, err)

			require.Equal(t, tc.expectedJSON, string(b))
		})
	}
}

func TestDescriptor_Append(t *testing.T) {
	existing := map[string]any{"a": "100", "b": "200"}
	d := New("c", map[string]any{"d": "300", "e": "400"})

	err := d.Merge(existing)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": "100", "b": "200", "c": map[string]any{"d": "300", "e": "400"}}, existing)
	require.ErrorIs(t, d.Merge(map[string]any{"c": "something else"}), ErrKeyAlreadyExists)
}

func TestDescriptor_Validate(t *testing.T) {
	type testCase struct {
		key   string
		props any
		err   error
	}

	testCases := []testCase{
		{"", []any{"a", "b", false, 10}, ErrKeyEmpty},
		{"invalid props", "boom", ErrSpecNotValidType},
		{"ok", []any{"a", "b", false, 10}, nil},
	}

	for _, tc := range testCases {
		t.Run(tc.key, func(t *testing.T) {
			d := &Descriptor{Key: tc.key, Props: tc.props}
			err := d.Validate()
			require.ErrorIs(t, err, tc.err)
		})
	}
}
