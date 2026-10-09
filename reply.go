package vlatte

import "context"

type Replier[I any, R any] interface {
	Reply(ctx context.Context, input I) (R, error)
}

// ReplierFactory makes the Replier of a mock. A mock builder calls NewReplier once for each mock it builds, with the
// server A it builds the mock for, so:
//   - each mock gets a Replier of its own: what it keeps, such as compiled templates or a position in a sequence,
//     belongs to that mock;
//   - one factory can serve many mocks and servers, and changing it later changes only the mocks built afterwards.
//
// NewReplier must not change the factory: mocks for different servers can be built at the same time. The Replier must
// not share a map or slice that the methods of the factory change. A factory that holds other factories calls their
// NewReplier.
type ReplierFactory[I any, R any, A any] interface {
	NewReplier(app A) (Replier[I, R], error)
}
