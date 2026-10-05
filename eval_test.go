package vlatte_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
)

func TestEval(t *testing.T) {
	makeFilter := func(name, label string) vlatte.Matcher {
		return match.Delegate(name, func(arg vlatte.MatcherArg) (bool, error) {
			arg.Explain().AddMessage(label + ": received value: " + fmt.Sprintf("%v", arg.Value()))
			return true, nil
		})
	}

	mat1 := match.All(makeFilter("test", "test01"), makeFilter("test", "test02"))

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

	explainer := vlatte.NewExplanation()
	sub := explainer.Sub("matchers")

	evals := vlatte.Evaluators[*fakeMock, any]{&ev1, &ev2}
	evals.Eval(t.Context(), sub, nil)
}
