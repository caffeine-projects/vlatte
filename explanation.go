package vlatte

// Explanation records why a mock matched a request or not, as a tree of named blocks.
// [MatcherArg.Explain] hands one to each matcher.
// A nil *Explanation records nothing; its methods are safe to call.
type Explanation struct {
	Name     string
	Items    []ExplanationItem
	Children []*Explanation

	// Parent is the block this one is a child of, or nil for the root. JSON leaves it out: it leads back here.
	Parent *Explanation `json:"-"`
}

// NewExplanation returns an empty root Explanation.
func NewExplanation() *Explanation {
	return &Explanation{Name: "root"}
}

// Root reports whether e is the root of its tree.
// A nil Explanation has no parent, so it reports true.
func (e *Explanation) Root() bool {
	return e == nil || e.Parent == nil
}

// Sub adds a child block with the given name and returns it.
// On a nil Explanation, it returns nil, which records nothing.
func (e *Explanation) Sub(name string) *Explanation {
	if e == nil {
		return nil
	}

	child := &Explanation{Parent: e, Name: name}
	e.Children = append(e.Children, child)

	return child
}

// AddMessage records a message in this block.
// On a nil Explanation, it does nothing.
func (e *Explanation) AddMessage(message string) {
	if e == nil {
		return
	}

	e.Items = append(e.Items, ExplanationItem{Message: message})
}

// Messages returns the messages of e, then those of each of its blocks, depth first. A template ranges over them,
// as it can't walk the blocks itself.
func (e *Explanation) Messages() []string {
	if e == nil {
		return nil
	}

	var out []string
	for _, item := range e.Items {
		out = append(out, item.Message)
	}

	for _, child := range e.Children {
		out = append(out, child.Messages()...)
	}

	return out
}

// ExplanationItem is a message recorded in an [Explanation].
type ExplanationItem struct {
	Message string
}
