package vlatte

import "context"

type Recipe[B Bean, A MockApp[B]] interface {
	Blend(ctx context.Context, pouch *Pouch[B, A]) error
}

type Pouch[B Bean, A MockApp[B]] struct {
	Brewer Brewer[B, A]
}

type RecipeFunc[B Bean, A MockApp[B]] func(ctx context.Context, pouch *Pouch[B, A]) error

func (f RecipeFunc[B, A]) Blend(ctx context.Context, pouch *Pouch[B, A]) error {
	return f(ctx, pouch)
}
