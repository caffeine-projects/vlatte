package vlatte

// noopExplanation collects the messages of matchers that run without an Explanation.
var noopExplanation = &Explanation{Name: "noop"}

// Explanation records why a mock matched a request or not, as a tree of named blocks.
// [MatcherArg.Explain] hands one to each matcher.
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
func (e *Explanation) Root() bool {
	return e.Parent == nil
}

// Sub adds a child block with the given name and returns it.
// On a nil Explanation, it returns a block that discards what it records.
func (e *Explanation) Sub(name string) *Explanation {
	if e == nil {
		return noopExplanation
	}

	child := &Explanation{Parent: e, Name: name}
	e.Children = append(e.Children, child)

	return child
}

// AddMessage records a message in this block.
func (e *Explanation) AddMessage(message string) {
	e.Items = append(e.Items, ExplanationItem{Message: message})
}

// ExplanationItem is a message recorded in an [Explanation].
type ExplanationItem struct {
	Message string
}
