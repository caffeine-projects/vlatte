package kcore

import (
	"context"
)

// PostActionInput represents the arguments that will be passed to every Callback implementation
type PostActionInput interface {
	Kind() string

	// Mock is the matched Mock for the current HTTP request.
	Mock() Mock

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
