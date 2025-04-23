package vlatte

import (
	"context"

	"github.com/caffeine-projects/vlatte/internal/encutil"
)

type Bean interface {
	ID() string
	Name() string
	Priority() int
	Origin() string
	Inc()
	Dec()
	Hits() int64
	Called() bool
	Roasted() bool
	Roast()
	Raw()
	Prepare()

	PostActions() PostActions
	Callbacks() []Callback
	Flow() []Step

	String() string
}

// Roaster describes a Mock builder.
type Roaster[B Bean, TMockApp MockApp[B]] interface {
	Roast(app TMockApp) (B, error)
}

type Canister[T Bean] interface {
	ScoopCuratedStream(ctx context.Context, done <-chan struct{}) (<-chan T, error)
	ScoopCurated(ctx context.Context) ([]T, error)

	Store(ctx context.Context, bean ...T) error

	Pick(ctx context.Context, id string) (T, error)
	PickAll(ctx context.Context) ([]T, error)

	Toss(ctx context.Context, ids ...string) error
	Empty(ctx context.Context) error
}

type Results struct {
	Buf []string
}

func (d *Results) Append(v string) {
	d.Buf = append(d.Buf, v)
}

func (d *Results) AppendList(sep string, v ...string) {
	d.Buf = append(d.Buf, encutil.Join(sep, v...))
}

func (d *Results) Len() int {
	return len(d.Buf)
}

func (d *Results) String() string {
	return encutil.Join("\n", d.Buf...)
}
