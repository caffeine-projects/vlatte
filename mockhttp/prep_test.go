package mockhttp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
)

func TestPrep(t *testing.T) {
	type recipe = vlatte.RecipeFunc[*Mock, *Server]
	type input = vlatte.RecipeInput[*Mock, *Server]

	t.Run("runs the recipes in order on this server", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		var ran []string

		err := m.Prep(t.Context(),
			recipe(func(_ context.Context, in *input) error {
				ran = append(ran, "first")
				_, err := in.Flavor.Mock(Getf("/from-recipe").Reply(OK()))
				return err
			}),
			recipe(func(context.Context, *input) error {
				ran = append(ran, "second")
				return nil
			}))

		require.NoError(t, err)
		require.Equal(t, []string{"first", "second"}, ran)
		require.Len(t, m.ListPending(), 1, "the mock added by a recipe belongs to the server")
	})

	t.Run("stops at the first recipe that fails", func(t *testing.T) {
		m := MustNew(Setup().Silent())
		errRecipe := errors.New("recipe failed")
		ranAfterFailure := false

		err := m.Prep(t.Context(),
			recipe(func(context.Context, *input) error { return nil }),
			recipe(func(context.Context, *input) error { return errRecipe }),
			recipe(func(context.Context, *input) error {
				ranAfterFailure = true
				return nil
			}))

		require.ErrorIs(t, err, errRecipe)
		require.ErrorContains(t, err, "index 1")
		require.False(t, ranAfterFailure, "recipes after a failing one must not run")
	})
}
