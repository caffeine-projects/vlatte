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

	// A block that reset left past the end is handed out again.
	if n := len(e.Children); n < cap(e.Children) {
		if child := e.Children[:n+1][n]; child != nil {
			child.reset()
			child.Parent, child.Name = e, name
			e.Children = e.Children[:n+1]

			return child
		}
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
// It returns nil when there are none.
func (e *Explanation) Messages() []string {
	n := e.count()
	if n == 0 {
		return nil
	}

	return e.appendMessages(make([]string, 0, n))
}

// count returns the number of messages of e and its blocks.
func (e *Explanation) count() int {
	if e == nil {
		return 0
	}

	n := len(e.Items)
	for _, child := range e.Children {
		n += child.count()
	}

	return n
}

// appendMessages appends the messages of e, then those of each of its blocks, depth first, to out.
func (e *Explanation) appendMessages(out []string) []string {
	for _, item := range e.Items {
		out = append(out, item.Message)
	}

	for _, child := range e.Children {
		out = child.appendMessages(out)
	}

	return out
}

// reset empties e, and keeps its blocks for [Explanation.Sub] to hand out again.
func (e *Explanation) reset() {
	if e == nil {
		return
	}

	e.Items = e.Items[:0]
	e.Children = e.Children[:0]
}

// ExplanationItem is a message recorded in an [Explanation].
type ExplanationItem struct {
	Message string
}
