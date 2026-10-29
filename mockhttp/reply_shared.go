package mockhttp

import "github.com/caffeine-projects/vlatte"

var (
	_ ReplierFactory         = sharedFactory{}
	_ vlatte.SelfDescriptive = sharedFactory{}
)

// Shared returns a ReplierFactory that gives r to every mock built with it: those mocks share r, and whatever it keeps.
// Use it for a Replier that keeps no state, such as one from another package. A Replier that keeps state, such as a
// counter, should come from a ReplierFactory of its own that makes one for each mock. The mocks never close r: when it
// implements io.Closer, closing it is the caller's.
func Shared(r Replier) ReplierFactory {
	return sharedFactory{r: r}
}

// sharedFactory gives the same Replier to every mock.
type sharedFactory struct {
	r Replier
}

// NewReplier implements ReplierFactory.
func (f sharedFactory) NewReplier(*Server) (Replier, error) {
	return f.r, nil
}

// Describe describes r, when r describes itself.
func (f sharedFactory) Describe() vlatte.Descriptor {
	d, _ := vlatte.IsSelfDescriptive(f.r)
	return d
}
