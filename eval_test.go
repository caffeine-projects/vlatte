package vlatte_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/vlattecore/explain"
)

var _ vlatte.Mock = (*testMock)(nil)

type testMock struct{}

func (b *testMock) ID() string                   { return "" }
func (b *testMock) Called() bool                 { return false }
func (b *testMock) Callbacks() []vlatte.Callback { return nil }
func (b *testMock) Name() string                 { return "" }
func (b *testMock) Priority() int                { return 0 }
func (b *testMock) Origin() string               { return "" }
func (b *testMock) Ext() any                     { return nil }
func (b *testMock) Inc()                         {}
func (b *testMock) Hits() int64                  { return 0 }
func (b *testMock) Enabled() bool                { return false }
func (b *testMock) Enable()                      {}
func (b *testMock) Disable()                     {}
func (b *testMock) Events() vlatte.Events        { return vlatte.Events{} }
func (b *testMock) Steps() []vlatte.Step         { return nil }
func (b *testMock) String() string               { return "" }

func TestEval(t *testing.T) {
	makeFilter := func(name, label string) vlatte.Filter {
		return filter.Delegate(name, func(arg vlatte.FilterArg) (bool, error) {
			arg.Explain().AddMessage(label + ": received value: " + fmt.Sprintf("%v", arg.Value()))
			return true, nil
		})
	}

	mat1 := filter.All(makeFilter("test", "test01"), makeFilter("test", "test02"))

	ev1 := vlatte.Eval[any]{
		Key:      "key",
		Target:   0,
		Matcher:  mat1,
		Selector: func(ctx context.Context, input any) any { return "ok" },
	}
	ev2 := vlatte.Eval[any]{
		Key:      "key",
		Target:   0,
		Matcher:  makeFilter("test", "test03"),
		Selector: func(ctx context.Context, input any) any { return "ok" },
	}

	explainer := explain.New()
	sub := explainer.Sub("matchers")

	evals := vlatte.Evaluators[*testMock, any]{&ev1, &ev2}
	evals.Eval(t.Context(), sub, nil)
}
