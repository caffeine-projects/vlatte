package vlatte

type Drink[B Bean, A MockApp[B]] interface {
	Brew(builders ...Builder[B, A]) (*Mug[B], error)
}
