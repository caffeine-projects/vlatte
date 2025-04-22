package vlatte

type Drink[B Bean, A MockApp[B]] interface {
	Brew(builders ...Roaster[B, A]) (*Mug[B], error)
}
