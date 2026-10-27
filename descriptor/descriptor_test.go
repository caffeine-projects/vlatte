package descriptor

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

var _ SelfDescriptive = selfDescribing{}

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

type selfDescribing struct {
	d Descriptor
}

func (s selfDescribing) Describe() Descriptor { return s.d }

func TestConstructors_RenderTheJSONCallersSerialize(t *testing.T) {
	inner := selfDescribing{d: New("inner", []any{"x"})}

	tests := []struct {
		name string
		give Descriptor
		want string
	}{
		{
			name: "single arg renders the key followed by the item",
			give: SingleArg("k", 1),
			want: `["k",1]`,
		},
		{
			name: "key only renders a one-element array",
			give: KeyOnly("k"),
			want: `["k"]`,
		},
		{
			name: "multi args renders the key followed by every arg",
			give: MultiArgs("k", "a", 2),
			want: `["k","a",2]`,
		},
		{
			name: "key value pairs render an object without the key",
			give: WithKeyValuePairs("k", "a", 1, "b", "x"),
			want: `{"a":1,"b":"x"}`,
		},
		{
			name: "key only maybe keeps the key and nests the self-descriptive item",
			give: KeyOnlyMaybe("k", inner),
			want: `["k",["inner","x"]]`,
		},
		{
			name: "key only maybe falls back to the key for a plain item",
			give: KeyOnlyMaybe("k", 42),
			want: `["k"]`,
		},
		{
			name: "maybe items keeps only self-descriptive items",
			give: WithMaybeItems("k", []any{inner, "plain", 3}),
			want: `["k",["inner","x"]]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, tt.give.Validate())

			b, err := tt.give.MarshalJSON()
			require.NoError(t, err)
			require.Equal(t, tt.want, string(b))
		})
	}
}

func TestConstructors_PanicOnEmptyKeyBecauseItIsAProgrammerError(t *testing.T) {
	t.Run("New", func(t *testing.T) {
		require.PanicsWithValue(t, ErrKeyEmpty, func() { New("", []any{"a"}) })
	})
	t.Run("MultiArgs", func(t *testing.T) {
		require.PanicsWithValue(t, ErrKeyEmpty, func() { MultiArgs("", "a") })
	})
	t.Run("WithKeyValuePairs", func(t *testing.T) {
		require.PanicsWithValue(t, ErrKeyEmpty, func() { WithKeyValuePairs("", "a", 1) })
	})
	t.Run("WithMaybeItems", func(t *testing.T) {
		require.PanicsWithValue(t, ErrKeyEmpty, func() { WithMaybeItems("", nil) })
	})
}

func TestWithKeyValuePairs_PanicsOnMalformedPairs(t *testing.T) {
	tests := []struct {
		name    string
		give    []any
		wantErr string
	}{
		{
			name:    "odd count",
			give:    []any{"a", 1, "b"},
			wantErr: "descriptor: expected an even number of key/value props. got: 3",
		},
		{
			name:    "non-string key reports its props index",
			give:    []any{"a", 1, 2, "b"},
			wantErr: "descriptor: expected a string key at index 2. got: int. value: 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.PanicsWithError(t, tt.wantErr, func() { WithKeyValuePairs("k", tt.give...) })
		})
	}
}

func TestRoot_MarksOnlyRootDescriptors(t *testing.T) {
	root := Root(map[string]any{"a": 1})
	require.True(t, root.Root())
	require.Equal(t, "root", root.Key)

	require.False(t, New("k", []any{}).Root())
}

func TestDescriptor_IsMapAndIsArrayTellPropsShapesApart(t *testing.T) {
	tests := []struct {
		name      string
		give      any
		wantMap   bool
		wantArray bool
	}{
		{
			name:      "map",
			give:      map[string]any{"a": 1},
			wantMap:   true,
			wantArray: false,
		},
		{
			name:      "slice",
			give:      []any{"a"},
			wantMap:   false,
			wantArray: true,
		},
		{
			name:      "nil",
			give:      nil,
			wantMap:   false,
			wantArray: false,
		},
		{
			name:      "descriptor",
			give:      New("inner", []any{"a"}),
			wantMap:   false,
			wantArray: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Descriptor{Key: "k", Props: tt.give}
			require.Equal(t, tt.wantMap, d.IsMap())
			require.Equal(t, tt.wantArray, d.IsArray())
		})
	}
}

func TestDescriptor_MergeRefusesNonMapPropsAndLeavesTargetUntouched(t *testing.T) {
	target := map[string]any{"a": 1}

	err := New("k", []any{"x"}).Merge(target)
	require.ErrorIs(t, err, ErrSpecNotValidType)
	require.Equal(t, map[string]any{"a": 1}, target)
}

func TestDescriptor_InvalidPropsAreRejectedNamingTheAcceptedTypes(t *testing.T) {
	d := Descriptor{Key: "k", Props: "boom"}
	wantMsg := "descriptor: spec is not a valid type: expected: []any, map[string]any, Descriptor or nil. got: string"

	require.EqualError(t, d.Validate(), wantMsg)

	m, err := d.Marshallable()
	require.ErrorIs(t, err, ErrSpecNotValidType)
	require.EqualError(t, err, wantMsg)
	require.Nil(t, m)

	b, err := d.MarshalJSON()
	require.ErrorIs(t, err, ErrSpecNotValidType)
	require.Nil(t, b)
}

func TestDescriptor_StringShowsKeyAndProps(t *testing.T) {
	require.Equal(t, "k([a 1])", New("k", []any{"a", 1}).String())
}

func TestIsSelfDescriptive_ReturnsTheDescriptionOnlyForSelfDescriptiveValues(t *testing.T) {
	want := New("inner", []any{"x"})

	got, ok := IsSelfDescriptive(selfDescribing{d: want})
	require.True(t, ok)
	require.Equal(t, want, got)

	got, ok = IsSelfDescriptive(42)
	require.False(t, ok)
	require.Equal(t, Descriptor{}, got)
}

func TestFail_EncodingFailsWithItsErrorAtAnyDepth(t *testing.T) {
	// Something a description can't hold, such as a function, must not describe as something else: encoding it, or
	// any description that holds it, fails with the reason it gives.
	errCant := errors.New("test: a function can't be described")
	failed := Fail("fn", errCant)

	require.ErrorIs(t, failed.Err(), errCant)
	require.ErrorIs(t, failed.Validate(), errCant)
	require.NoError(t, New("ok", []any{1}).Err())

	_, err := failed.MarshalJSON()
	require.ErrorIs(t, err, errCant)

	for name, holder := range map[string]Descriptor{
		"list": New("seq", []any{New("ok", []any{1}), failed}),
		"map":  Root(map[string]any{"reply": failed}),
	} {
		_, err := holder.MarshalJSON()
		require.ErrorIs(t, err, errCant, name)
	}
}
