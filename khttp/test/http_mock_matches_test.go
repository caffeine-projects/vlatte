package test

// import (
// 	"context"
// 	"fmt"
// 	"testing"

// 	"github.com/caffeine-projects/vlatte/kcore"
// 	"github.com/caffeine-projects/vlatte/khttp"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"

// 	. "github.com/caffeine-projects/vlatte/matcher"
// )

// func TestMockMatches(t *testing.T) {
// 	params := &khttp.HTTPValueSelectorInput{}

// 	testCases := []struct {
// 		name     string
// 		value    any
// 		selector any
// 		expected bool
// 	}{
// 		{value: "test", selector: "test", expected: true},
// 		{value: 10.0, selector: 10.0, expected: true},
// 		{value: true, selector: true, expected: true},
// 		{value: map[string]any{"key": "value"}, selector: map[string]any{"key": "value"}, expected: true},
// 		{value: "test", selector: "dev", expected: false},
// 		{value: make(chan struct{}, 1), selector: nil, expected: false},
// 	}

// 	for _, tc := range testCases {
// 		tc := tc
// 		t.Run(tc.name, func(t *testing.T) {
// 			evals := kcore.Evaluators[*khttp.HTTPMock, *khttp.HTTPValueSelectorInput]([]*kcore.Expectation[*khttp.HTTPValueSelectorInput]{{
// 				Matcher: StrictEqual(tc.value),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return tc.selector
// 				},
// 			}})
// 			evals.Eval(context.Background(), &khttp.HTTPValueSelectorInput{}, func(m *khttp.HTTPMock) kcore.Evaluators[*khttp.HTTPMock, *khttp.HTTPValueSelectorInput] {
// 				return m.Expectations
// 			})

// 			require.Equal(t, tc.expected, pass)
// 		})
// 	}

// 	t.Run("should return not matched and error when one of expectations returns error", func(t *testing.T) {
// 		// string
// 		desc := &kcore.Results{}
// 		pass, _ := kcore.Match(context.Background(), params, desc, []*kcore.Expectation[*khttp.HTTPValueSelectorInput]{{
// 			Matcher: Function(func(_ any) (bool, error) {
// 				return false, fmt.Errorf("fail")
// 			}),
// 			Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 				return "dev"
// 			},
// 		}}, kcore.FindOptions{})

// 		require.False(t, pass)
// 	})

// 	t.Run("should not pass when it panics", func(t *testing.T) {
// 		// string
// 		desc := &kcore.Results{}
// 		pass, _ := kcore.Match(context.Background(), params, desc, []*kcore.Expectation[*khttp.HTTPValueSelectorInput]{{
// 			Matcher: Function(func(_ any) (bool, error) {
// 				panic("boom!")
// 			}),
// 			Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 				return "dev"
// 			},
// 		}}, kcore.FindOptions{})

// 		require.False(t, pass)
// 	})

// 	t.Run("should return the sum of the matchers Weight when it matches", func(t *testing.T) {
// 		// any
// 		desc := &kcore.Results{}
// 		pass, weigth := kcore.Match(context.Background(), params, desc, []*kcore.Expectation[*khttp.HTTPValueSelectorInput]{
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 2,
// 			},
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 1,
// 			},
// 			{
// 				Matcher: StrictEqual(10.0),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return 10.0
// 				},
// 				Weight: 2,
// 			},
// 		}, kcore.FindOptions{})

// 		assert.True(t, pass)
// 		assert.Equal(t, 5, weigth)
// 	})

// 	t.Run("should return the sum of the matchers Weight when one of then doesn't match", func(t *testing.T) {
// 		// any
// 		desc := &kcore.Results{}
// 		pass, weight := kcore.Match(context.Background(), params, desc, []*kcore.Expectation[*khttp.HTTPValueSelectorInput]{
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 2,
// 			},
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return "dev"
// 				},
// 				Weight: 1,
// 			},
// 			{
// 				Matcher: StrictEqual(10.0),
// 				Selector: func(_ context.Context, r *khttp.HTTPValueSelectorInput) any {
// 					return 10.0
// 				},
// 				Weight: 2,
// 			},
// 		}, kcore.FindOptions{})

// 		assert.False(t, pass)
// 		assert.Equal(t, 4, weight)
// 	})
// }
