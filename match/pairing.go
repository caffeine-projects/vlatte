package match

// pairUp reports whether each of the want items pairs with one of the got items of its own, by fits(w, g): whether
// want item w fits got item g. fits needn't be transitive, as Equal isn't ("1.0" and "1" both equal 1 but not each
// other), so a greedy pairing could miss one that exists: this finds a maximum matching instead, with augmenting
// paths. An error of fits stops it.
func pairUp(want, got int, fits func(w, g int) (bool, error)) (bool, error) {
	fit := make([][]bool, want)
	for w := range want {
		fit[w] = make([]bool, got)
		for g := range got {
			ok, err := fits(w, g)
			if err != nil {
				return false, err
			}

			fit[w][g] = ok
		}
	}

	pair := make([]int, got) // the want item each got item is paired with, or -1
	for g := range pair {
		pair[g] = -1
	}

	var augment func(w int, seen []bool) bool

	augment = func(w int, seen []bool) bool {
		for g := range got {
			if seen[g] || !fit[w][g] {
				continue
			}

			seen[g] = true

			if pair[g] < 0 || augment(pair[g], seen) {
				pair[g] = w
				return true
			}
		}

		return false
	}

	for w := range want {
		if !augment(w, make([]bool, got)) {
			return false, nil
		}
	}

	return true, nil
}
