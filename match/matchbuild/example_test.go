package matchbuild_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

func ExampleWithMatcher() {
	// [longer-than, n] matches text longer than n bytes.
	registry := matchbuild.MustNew(matchbuild.WithMatcher("longer-than",
		func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
			if len(args) != 1 {
				return nil, errors.New("takes a length")
			}

			n, ok := args[0].(int64)
			if !ok {
				return nil, errors.New("takes a length")
			}

			return match.Function(func(v any) (bool, error) {
				s, isText := v.(string)
				return isText && int64(len(s)) > n, nil
			}), nil
		}))

	m, err := registry.BuildMatcher([]any{"longer-than", int64(3)})
	if err != nil {
		panic(err)
	}

	for _, v := range []string{"abc", "abcd"} {
		ok, _ := m.Match(vlatte.NewMatcherArg(context.Background(), vlatte.NewExplanation(), v))
		fmt.Println(v, ok)
	}

	// Output:
	// abc false
	// abcd true
}
