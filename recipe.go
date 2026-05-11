package vlatte

import "context"

type Recipe[B Mock, A MockApp[B]] interface {
	Prep(ctx context.Context, input *RecipeInput[B, A]) error
}

type RecipeInput[B Mock, A MockApp[B]] struct {
	Flavor Flavor[B, A]
}

type RecipeFunc[B Mock, A MockApp[B]] func(ctx context.Context, input *RecipeInput[B, A]) error

func (f RecipeFunc[B, A]) Prep(ctx context.Context, input *RecipeInput[B, A]) error {
	return f(ctx, input)
}
