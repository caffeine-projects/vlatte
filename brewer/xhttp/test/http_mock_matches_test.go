package test

// import (
// 	"context"
// 	"fmt"
// 	"testing"

// 	"github.com/caffeine-projects/vlatte/kcore"
// 	"github.com/caffeine-projects/vlatte/brewer/xhttp"
// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"

// 	. "github.com/caffeine-projects/vlatte/matcher"
// )

// func TestMockMatches(t *testing.T) {
// 	params := &xhttp.HTTPValueSelectorInput{}

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
// 			evals := vlatte.Evaluators[*xhttp.HTTPMock, *xhttp.HTTPValueSelectorInput]([]*vlatte.Expectation[*xhttp.HTTPValueSelectorInput]{{
// 				Matcher: StrictEqual(tc.value),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return tc.selector
// 				},
// 			}})
// 			evals.Eval(context.Background(), &xhttp.HTTPValueSelectorInput{}, func(m *xhttp.HTTPMock) vlatte.Evaluators[*xhttp.HTTPMock, *xhttp.HTTPValueSelectorInput] {
// 				return m.Expectations
// 			})

// 			require.Equal(t, tc.expected, pass)
// 		})
// 	}

// 	t.Run("should return not matched and error when one of expectations returns error", func(t *testing.T) {
// 		// string
// 		desc := &vlatte.Results{}
// 		pass, _ := vlatte.Match(context.Background(), params, desc, []*vlatte.Expectation[*xhttp.HTTPValueSelectorInput]{{
// 			Matcher: Function(func(_ any) (bool, error) {
// 				return false, fmt.Errorf("fail")
// 			}),
// 			Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 				return "dev"
// 			},
// 		}}, vlatte.FindOptions{})

// 		require.False(t, pass)
// 	})

// 	t.Run("should not pass when it panics", func(t *testing.T) {
// 		// string
// 		desc := &vlatte.Results{}
// 		pass, _ := vlatte.Match(context.Background(), params, desc, []*vlatte.Expectation[*xhttp.HTTPValueSelectorInput]{{
// 			Matcher: Function(func(_ any) (bool, error) {
// 				panic("boom!")
// 			}),
// 			Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 				return "dev"
// 			},
// 		}}, vlatte.FindOptions{})

// 		require.False(t, pass)
// 	})

// 	t.Run("should return the sum of the matchers Weight when it matches", func(t *testing.T) {
// 		// any
// 		desc := &vlatte.Results{}
// 		pass, weigth := vlatte.Match(context.Background(), params, desc, []*vlatte.Expectation[*xhttp.HTTPValueSelectorInput]{
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 2,
// 			},
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 1,
// 			},
// 			{
// 				Matcher: StrictEqual(10.0),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return 10.0
// 				},
// 				Weight: 2,
// 			},
// 		}, vlatte.FindOptions{})

// 		assert.True(t, pass)
// 		assert.Equal(t, 5, weigth)
// 	})

// 	t.Run("should return the sum of the matchers Weight when one of then doesn't match", func(t *testing.T) {
// 		// any
// 		desc := &vlatte.Results{}
// 		pass, weight := vlatte.Match(context.Background(), params, desc, []*vlatte.Expectation[*xhttp.HTTPValueSelectorInput]{
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return "test"
// 				},
// 				Weight: 2,
// 			},
// 			{
// 				Matcher: StrictEqual("test"),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return "dev"
// 				},
// 				Weight: 1,
// 			},
// 			{
// 				Matcher: StrictEqual(10.0),
// 				Selector: func(_ context.Context, r *xhttp.HTTPValueSelectorInput) any {
// 					return 10.0
// 				},
// 				Weight: 2,
// 			},
// 		}, vlatte.FindOptions{})

// 		assert.False(t, pass)
// 		assert.Equal(t, 4, weight)
// 	})
// }
