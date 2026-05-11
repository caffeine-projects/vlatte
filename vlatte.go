package vlatte

import "context"

// FlavorFactory creates a mock app of type A handling mocks of type B.
// Implement this in each flavor package to plug into [New].
type FlavorFactory[B Mock, A MockApp[B]] interface {
	Create(ctx context.Context) A
}

// New initializes a mock app for the given flavor and applies any recipes.
// The concrete flavor type is preserved in the return value so all
// flavor-specific methods remain accessible without type assertions.
//
//	m, err := vlatte.New(ctx, xhttp.Flavor())
//	m, err := vlatte.New(ctx, xhttp.Flavor(xhttp.Setup().CORS()), proxyall.New(config))
func New[B Mock, A interface {
	MockApp[B]
	Mock(builders ...Builder[B, A]) (*Scope[B], error)
}](ctx context.Context, factory FlavorFactory[B, A], recipes ...Recipe[B, A]) (A, error) {
	app := factory.Create(ctx)
	for _, r := range recipes {
		if err := r.Prep(ctx, &RecipeInput[B, A]{Flavor: app}); err != nil {
			return app, err
		}
	}
	return app, nil
}
