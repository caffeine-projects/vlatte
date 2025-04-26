package vlatte

type Sleeve[B Bean] struct {
	cup      *Cup[B]
	canister *Canister[B]
}
