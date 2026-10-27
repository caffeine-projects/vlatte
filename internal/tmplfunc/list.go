package tmplfunc

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"

	"github.com/ohler55/ojg/jp"
)

// errEmptyList says randomItem got a list without items.
var errEmptyList = errors.New("the list is empty")

// listItems returns the items of a list or an array, in a new []any. nil is a list without items.
func listItems(list any) ([]any, error) {
	if list == nil {
		return []any{}, nil
	}

	if items, ok := list.([]any); ok {
		return slices.Clone(items), nil
	}

	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, fmt.Errorf("%T is not a list", list)
	}

	items := make([]any, v.Len())
	for i := range items {
		items[i] = v.Index(i).Interface()
	}

	return items, nil
}

// list returns its arguments as a list. Without any, a template gives it an empty list, not nil, so toJSON writes [].
func list(items ...any) []any {
	return items
}

// appendItem returns a new list of the items of list, then item.
func appendItem(list, item any) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	return append(items, item), nil
}

// insertItem returns a new list of the items of list with item at index: 0 is the start, and the length the end.
func insertItem(list any, index int, item any) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	if index < 0 || index > len(items) {
		return nil, fmt.Errorf("index %d is out of [0:%d]", index, len(items))
	}

	return slices.Insert(items, index, item), nil
}

// removeItem returns a new list of the items of list but the one at index. A negative index counts from the end: -1
// is the last item.
func removeItem(list any, index int) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	i := index
	if i < 0 {
		i += len(items)
	}

	if i < 0 || i >= len(items) {
		return nil, fmt.Errorf("index %d is out of a list of %d items", index, len(items))
	}

	return slices.Delete(items, i, i+1), nil
}

// randomItem returns an item of list, at random.
func randomItem(list any) (any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	if len(items) == 0 {
		return nil, errEmptyList
	}

	return items[rand.IntN(len(items))], nil
}

// shuffle returns a new list of the items of list, in random order.
func shuffle(list any) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })

	return items, nil
}

// reverse returns a new list of the items of list, last first.
func reverse(list any) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	slices.Reverse(items)

	return items, nil
}

// sortBy returns a new list of the items of list, sorted by the value path selects in each, smallest first, and
// stable. An item path selects nothing in comes first. The values must all be numbers, all strings or all booleans,
// false first.
func sortBy(list any, path string) ([]any, error) {
	items, err := listItems(list)
	if err != nil {
		return nil, err
	}

	x, err := jp.ParseString(path)
	if err != nil {
		return nil, err
	}

	keys := make([]any, len(items))
	for i, item := range items {
		if all := selectAll(x, item); len(all) > 0 {
			keys[i] = all[0]
		}
	}

	compare, err := keyOrder(keys)
	if err != nil {
		return nil, err
	}

	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int { return compare(keys[a], keys[b]) })

	sorted := make([]any, len(items))
	for i, o := range order {
		sorted[i] = items[o]
	}

	return sorted, nil
}

// keyOrder returns how sortBy orders keys, nil before any other, or an error when the others are not all of one kind.
func keyOrder(keys []any) (func(a, b any) int, error) {
	var kind string

	for _, k := range keys {
		var kk string

		switch k.(type) {
		case nil:
			continue
		case string:
			kk = "string"
		case bool:
			kk = "bool"
		default:
			if _, err := toNumber(k); err != nil {
				return nil, fmt.Errorf("can't sort by %T values", k)
			}

			kk = "number"
		}

		if kind != "" && kind != kk {
			return nil, fmt.Errorf("can't sort by both %s and %s values", kind, kk)
		}

		kind = kk
	}

	return func(a, b any) int {
		switch {
		case a == nil || b == nil:
			return cmp.Compare(boolInt(a != nil), boolInt(b != nil))
		case kind == "string":
			x, _ := a.(string)
			y, _ := b.(string)

			return cmp.Compare(x, y)
		case kind == "bool":
			x, _ := a.(bool)
			y, _ := b.(bool)

			return cmp.Compare(boolInt(x), boolInt(y))
		default:
			x, _ := toNumber(a)
			y, _ := toNumber(b)

			if !x.isFloat && !y.isFloat {
				return cmp.Compare(x.i, y.i)
			}

			return cmp.Compare(x.float(), y.float())
		}
	}, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}
