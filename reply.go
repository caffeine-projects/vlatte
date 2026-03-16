package vlatte

import "context"

type Replier[I any, R any] interface {
	Reply(ctx context.Context, input I) (R, error)
}

type Mapper[I any, O any] interface {
	Map(ctx context.Context, input I) (O, error)
}

type MapperFunc[I any, O any] func(ctx context.Context, input I) (O, error)

func (f MapperFunc[I, O]) Map(ctx context.Context, input I) (O, error) {
	return f(ctx, input)
}

func Reply[I any, O any](ctx context.Context, input I, mappers []Mapper[I, O]) (O, error) {
	var output O
	for _, mapper := range mappers {
		var err error
		output, err = mapper.Map(ctx, input)
		if err != nil {
			var zero O
			return zero, err
		}
	}

	return output, nil
}
