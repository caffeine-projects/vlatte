package explain

func New() *Block {
	return &Block{Name: "root"}
}

type Block struct {
	Name   string
	Items  []BlockItem
	Child  *Block
	Parent *Block
}

func (b *Block) Root() bool {
	return b.Parent == nil
}

func (b *Block) Sub() *Block {
	if b.Child == nil {
		b.Child = &Block{Parent: b}
	}

	return b.Child
}

type BlockItem struct {
	Message string
}
