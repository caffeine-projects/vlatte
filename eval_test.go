package vlatte_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/filter"
	"github.com/caffeine-projects/vlatte/shelf/explain"
)

var _ vlatte.Bean = (*bean)(nil)

type bean struct {
}

func (b *bean) ID() string                   { return "" }
func (b *bean) Called() bool                 { return false }
func (b *bean) Callbacks() []vlatte.Callback { return nil }
func (b *bean) Name() string                 { return "" }
func (b *bean) Priority() int                { return 0 }
func (b *bean) Origin() string               { return "" }
func (b *bean) Ext() any                     { return nil }
func (b *bean) Inc()                         {}
func (b *bean) Hits() int64                  { return 0 }
func (b *bean) Roasted() bool                { return false }
func (b *bean) Roast()                       {}
func (b *bean) Raw()                         {}
func (b *bean) Events() vlatte.Events        { return vlatte.Events{} }
func (b *bean) Steps() []vlatte.Step         { return nil }
func (b *bean) String() string               { return "" }

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

	evals := vlatte.Evaluators[*bean, any]{&ev1, &ev2}
	evals.Eval(t.Context(), sub, nil)
}
