package vlatte

import (
	"context"
)

// PostActionInput represents the arguments that will be passed to every Callback implementation
type PostActionInput interface {
	Kind() Kind

	// Bean is the matched Bean for the current HTTP request.
	Bean() Bean

	// Args allow passing custom arguments to a PostAction.
	Args() any
}

// PostAction defines the contract for an action that will be executed after serving a mocked HTTP response.
type PostAction interface {
	// Run runs the Callback implementation.
	Run(ctx context.Context, input PostActionInput) error
}

type PostActionDefinition struct {
	Name       string
	Parameters any
}

type PostActions []PostActionDefinition

type PostActionFunc func(ctx context.Context, input PostActionInput) error

func (f PostActionFunc) Run(ctx context.Context, input PostActionInput) error {
	return f(ctx, input)
}
