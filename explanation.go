package vlatte

// Explanation records why a mock matched a request or not, as a tree of named blocks.
// [MatcherArg.Explain] hands one to each matcher.
// A nil *Explanation records nothing; its methods are safe to call.
type Explanation struct {
	Name     string
	Items    []ExplanationItem
	Children []*Explanation
	Parent   *Explanation
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

// ExplanationItem is a message recorded in an [Explanation].
type ExplanationItem struct {
	Message string
}
