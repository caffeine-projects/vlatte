package explain

var noop = &Block{Name: "noop"}

type Block struct {
	Name     string
	Items    []Item
	Children []*Block
	Parent   *Block
}

func New() *Block {
	return &Block{Name: "root"}
}

func (b *Block) Root() bool {
	return b.Parent == nil
}

func (b *Block) Sub(name string) *Block {
	if b == nil {
		return noop
	}

	child := &Block{Parent: b, Name: name}
	b.Children = append(b.Children, child)

	return child
}

func (b *Block) AddMessage(message string) {
	b.Items = append(b.Items, Item{Message: message})
}

type Item struct {
	Message string
}
