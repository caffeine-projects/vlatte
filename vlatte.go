package vlatte

import "context"

// FlavorFactory creates a mock flavor of type A handling mocks of type B.
// Implement this in each flavor package to plug into [New].
type FlavorFactory[B Mock, A Flavor[B, A]] interface {
	Create(ctx context.Context) (A, error)
}

// New initializes a mock flavor for the given factory and applies any recipes.
// The concrete flavor type is preserved in the return value so all
// flavor-specific methods remain accessible without type assertions.
//
//	m, err := vlatte.New(ctx, xhttp.Flavor())
//	m, err := vlatte.New(ctx, xhttp.Flavor(xhttp.Setup().CORS()), proxyall.New(config))
func New[B Mock, A Flavor[B, A]](ctx context.Context, factory FlavorFactory[B, A], recipes ...Recipe[B, A]) (A, error) {
	app, err := factory.Create(ctx)
	if err != nil {
		return app, err
	}

	for _, r := range recipes {
		if err := r.Prep(ctx, &RecipeInput[B, A]{Flavor: app}); err != nil {
			return app, err
		}
	}
	return app, nil
}
