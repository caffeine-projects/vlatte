package vlatte

import "context"

type Recipe[B Mock, A Server[B, A]] interface {
	Prep(ctx context.Context, input *RecipeInput[B, A]) error
}

type RecipeInput[B Mock, A Server[B, A]] struct {
	Server Server[B, A]
}

type RecipeFunc[B Mock, A Server[B, A]] func(ctx context.Context, input *RecipeInput[B, A]) error

func (f RecipeFunc[B, A]) Prep(ctx context.Context, input *RecipeInput[B, A]) error {
	return f(ctx, input)
}
