package vlatte

import "context"

type PipelineDeps[
	REQ any,
	PREQ any,
] interface {
	RequestParser() RequestParser[PREQ, any]
}

type Pipeline[REQ any, PREQ any] struct {
	Deps PipelineDeps[REQ, PREQ]
}

func (p *Pipeline[REQ, PREQ]) Run(ctx context.Context, req REQ, writer any) error {
	return nil
}
