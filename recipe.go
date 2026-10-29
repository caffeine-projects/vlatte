package vlatte

import "context"

// Recipe prepares a mock server, such as by adding mocks to it. The Prep method of a server runs recipes in order, and
// stops at the first that fails.
type Recipe[B Mock, A Server[B, A]] interface {
	Prep(ctx context.Context, input *RecipeInput[B, A]) error
}

// RecipeInput is what a [Recipe] prepares: Server is the mock server.
type RecipeInput[B Mock, A Server[B, A]] struct {
	Server Server[B, A]
}

// RecipeFunc is a function that implements [Recipe].
type RecipeFunc[B Mock, A Server[B, A]] func(ctx context.Context, input *RecipeInput[B, A]) error

// Prep implements [Recipe].
func (f RecipeFunc[B, A]) Prep(ctx context.Context, input *RecipeInput[B, A]) error {
	return f(ctx, input)
}
