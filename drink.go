package vlatte

type Drink[B Bean, A MockApp[B]] interface {
	Brew(builders ...Grinder[B, A]) (*Cup[B], error)
}
