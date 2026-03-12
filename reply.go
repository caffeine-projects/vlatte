package vlatte

import "context"

type Replier[I any, R any] interface {
	Reply(ctx context.Context, input I) (R, error)
}
