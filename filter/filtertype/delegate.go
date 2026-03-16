package filtertype

import "github.com/caffeine-projects/vlatte"

var _ vlatte.Filter = (*Delegate)(nil)

type Delegate struct {
	name      string
	predicate func(arg vlatte.FilterArg) (bool, error)
}

func NewDelegate(name string, predicate func(arg vlatte.FilterArg) (bool, error)) *Delegate {
	return &Delegate{name: name, predicate: predicate}
}

func (d *Delegate) Name() string {
	return d.name
}

func (d *Delegate) Match(arg vlatte.FilterArg) (bool, error) {
	return d.predicate(arg)
}
