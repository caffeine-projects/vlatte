package vlatte

type Brewer[B Bean, A MockApp[B]] interface {
	Brew(builders ...Builder[B, A]) (*Mug[B], error)
}
