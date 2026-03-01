package vlatte

import (
	"context"
	"iter"

	"github.com/caffeine-projects/vlatte/internal/encutil"
)

type Bean interface {
	ID() string
	Name() string
	Priority() int
	Origin() string
	Ext() any
	Inc()
	Hits() int64
	Called() bool
	Roasted() bool
	Roast()
	Raw()

	// DEPRECATED: being changed to Hooks.
	PostActions() PostActions
	Buzzers() *Buzzers
	Callbacks() []Callback
	Flow() []Step

	String() string
}

// Grinder describes a Mock builder.
type Grinder[B Bean, TMockApp MockApp[B]] interface {
	Grind(app TMockApp) (B, error)
}

type Canister[T Bean] interface {
	Scoop(ctx context.Context) (iter.Seq[T], error)

	Store(ctx context.Context, bean ...T) error

	Pick(ctx context.Context, id string) (T, error)
	PickAll(ctx context.Context) ([]T, error)

	Toss(ctx context.Context, ids ...string) error
	Empty(ctx context.Context) error
}

type Buzzers struct {
	OnComplete []OnComplete
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
