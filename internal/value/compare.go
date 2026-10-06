package value

import (
	"cmp"
	"math"
	"math/big"
	"reflect"
)

// Equal reports whether a and b are equal.
//
// Values of one class compare by value: bools and text as they are, numbers by their exact value whatever their
// Go types, lists item by item and in order, objects member by member, and other values with reflect.DeepEqual.
// Across classes, text compared with a number reads as one with ParseNumber, and anything compared with a bool
// reads as one with Bool: "10" equals 10, and "t", 1 and true are equal. Nothing else is equal across classes.
//
// Equal is symmetric, but not transitive: "1.0" and "1" both equal 1, yet not each other.
func Equal(a, b any) bool {
	return equal(classify(a), classify(b), true)
}

// Same reports whether a and b are equal without reading text as a number or anything as a bool:
// they must be of one class. Numbers still compare by their exact value, and lists and objects by their items,
// whatever their Go types.
func Same(a, b any) bool {
	return equal(classify(a), classify(b), false)
}

func equal(x, y kept, read bool) bool {
	if x.class != y.class {
		return read && across(x, y)
	}

	switch x.class {
	case classNil:
		return true
	case classBool:
		return x.b == y.b
	case classText:
		return x.s == y.s
	case classNumber:
		c, ok := compareNumbers(x.n, y.n)
		return ok && c == 0
	case classList:
		if len(x.list) != len(y.list) {
			return false
		}

		for i := range x.list {
			if !equal(classify(x.list[i]), classify(y.list[i]), read) {
				return false
			}
		}

		return true
	case classObject:
		if len(x.obj) != len(y.obj) {
			return false
		}

		for k, xv := range x.obj {
			yv, ok := y.obj[k]
			if !ok || !equal(classify(xv), classify(yv), read) {
				return false
			}
		}

		return true
	default:
		return reflect.DeepEqual(x.other, y.other)
	}
}

// across compares values of two classes: anything compared with a bool reads as a bool,
// and text compared with a number reads as a number.
func across(x, y kept) bool {
	switch {
	case x.class == classBool:
		b, ok := boolOf(y)
		return ok && b == x.b
	case y.class == classBool:
		b, ok := boolOf(x)
		return ok && b == y.b
	case x.class == classText && y.class == classNumber, x.class == classNumber && y.class == classText:
		c, ok := compareKept(x, y)
		return ok && c == 0
	default:
		return false
	}
}

// Compare orders a and b, each a number or text that ParseNumber reads, by their exact values:
// -1 when a is less than b, 0 when they are equal and +1 when a is greater. ok is false for other values, and NaN.
func Compare(a, b any) (int, bool) {
	return compareKept(classify(a), classify(b))
}

func compareKept(x, y kept) (int, bool) {
	a, ok := numberOf(x)
	if !ok {
		return 0, false
	}

	b, ok := numberOf(y)
	if !ok {
		return 0, false
	}

	return compareNumbers(a, b)
}

// numberOf returns a number, or text that ParseNumber reads, as a number.
func numberOf(k kept) (any, bool) {
	switch k.class {
	case classNumber:
		return k.n, true
	case classText:
		return ParseNumber(k.s)
	default:
		return nil, false
	}
}

// compareNumbers orders two numbers as Number gives them, by their exact values. ok is false when one is NaN.
func compareNumbers(a, b any) (int, bool) {
	switch x := a.(type) {
	case int64:
		if y, ok := b.(int64); ok {
			return cmp.Compare(x, y), true
		}
	case float64:
		if y, ok := b.(float64); ok && !math.IsNaN(x) && !math.IsNaN(y) {
			return cmp.Compare(x, y), true
		}
	}

	x, ok := exact(a)
	if !ok {
		return 0, false
	}

	y, ok := exact(b)
	if !ok {
		return 0, false
	}

	return x.Cmp(y), true
}

// exact returns a number as Number gives it, an int64, a uint64 or a float64, as a big.Float of the same value.
// ok is false for NaN.
func exact(n any) (*big.Float, bool) {
	switch x := n.(type) {
	case int64:
		return new(big.Float).SetInt64(x), true
	case uint64:
		return new(big.Float).SetUint64(x), true
	}

	f, _ := n.(float64)
	if math.IsNaN(f) {
		return nil, false
	}

	return big.NewFloat(f), true
}

// Len returns the length of v: the bytes of text, the items of a list or the members of an object.
// ok is false for other values.
func Len(v any) (int, bool) {
	switch k := classify(v); k.class {
	case classText:
		return len(k.s), true
	case classList:
		return len(k.list), true
	case classObject:
		return len(k.obj), true
	default:
		return 0, false
	}
}
