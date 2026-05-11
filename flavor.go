package vlatte

type Flavor[B Mock, A MockApp[B]] interface {
	Mock(builders ...Builder[B, A]) (*Scope[B], error)
}
