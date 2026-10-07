package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"pgregory.net/rapid"
)

// nestedItem and nestedDoc model a document with nested objects, lists of objects and maps,
// the shapes mock files have.
type nestedItem struct {
	Name string
	Size int64
	Data any
}

type nestedDoc struct {
	Title  string
	Count  int64
	Wait   time.Duration
	On     bool
	Tags   []string
	Item   *nestedItem
	Items  []nestedItem
	Labels map[string]string
	Raw    any
}

func itemSchema() *Schema[nestedItem] {
	return NewSchema(
		Field[nestedItem]{
			Key: "name", Kind: String, Usage: "Name.",
			Set: func(i *nestedItem, v Value) error { i.Name = v.Str; return nil },
		},
		Field[nestedItem]{
			Key: "size", Kind: Int, Usage: "Size.",
			Set: func(i *nestedItem, v Value) error {
				if v.Int < 0 {
					return errors.New("must be 0 or more")
				}

				i.Size = v.Int

				return nil
			},
		},
		Field[nestedItem]{
			Key: "meta.data", Kind: Any, Usage: "Data.",
			Set: func(i *nestedItem, v Value) error { i.Data = v.Any; return nil },
		},
	)
}

func nestedSchema() *Schema[nestedDoc] {
	items := itemSchema()

	return NewSchema(
		Field[nestedDoc]{
			Key: "title", Kind: String, Usage: "Title.",
			Set: func(d *nestedDoc, v Value) error { d.Title = v.Str; return nil },
		},
		Field[nestedDoc]{
			Key: "count", Kind: Int, Usage: "Count.",
			Set: func(d *nestedDoc, v Value) error { d.Count = v.Int; return nil },
		},
		Field[nestedDoc]{
			Key: "wait", Kind: Duration, Usage: "Wait.",
			Set: func(d *nestedDoc, v Value) error { d.Wait = v.Dur; return nil },
		},
		Field[nestedDoc]{
			Key: "on", Kind: Bool, Usage: "On.",
			Set: func(d *nestedDoc, v Value) error { d.On = v.Bool; return nil },
		},
		Field[nestedDoc]{
			Key: "tags", Kind: List, Usage: "Tags.",
			Set: func(d *nestedDoc, v Value) error { d.Tags = v.List; return nil },
		},
		Field[nestedDoc]{
			Key: "item", Kind: Any, Usage: "Item.",
			Set: func(d *nestedDoc, v Value) error {
				d.Item = new(nestedItem)
				return items.Decode(d.Item, v)
			},
		},
		Field[nestedDoc]{
			Key: "items", Kind: Any, Usage: "Items.",
			Set: func(d *nestedDoc, v Value) error {
				return Items(v, func(e Value) error {
					var it nestedItem
					if err := items.Decode(&it, e); err != nil {
						return err
					}

					d.Items = append(d.Items, it)

					return nil
				})
			},
		},
		Field[nestedDoc]{
			Key: "labels", Kind: Any, Usage: "Labels.",
			Set: func(d *nestedDoc, v Value) error {
				d.Labels = make(map[string]string)

				return Members(v, func(name string, m Value) error {
					s, ok := m.Any.(string)
					if !ok {
						return fmt.Errorf("must be text, not %s", Describe(m.Any))
					}

					d.Labels[name] = s

					return nil
				})
			},
		},
		Field[nestedDoc]{
			Key: "raw", Kind: Any, Usage: "Raw.",
			Set: func(d *nestedDoc, v Value) error { d.Raw = v.Any; return nil },
		},
	)
}

// nestedCase is a valid document and the struct it must load into.
type nestedCase struct {
	doc  map[string]any
	want nestedDoc
}

// itemCase is a valid item object and the item it must load into.
type itemCase struct {
	doc  map[string]any
	want nestedItem
}

func genItem() *rapid.Generator[itemCase] {
	return rapid.Custom(func(t *rapid.T) itemCase {
		c := itemCase{doc: make(map[string]any)}

		if rapid.Bool().Draw(t, "has name") {
			c.want.Name = genText().Draw(t, "name")
			c.doc["name"] = c.want.Name
		}

		if rapid.Bool().Draw(t, "has size") {
			c.want.Size = rapid.Int64Range(0, _maxExactInt).Draw(t, "size")
			c.doc["size"] = c.want.Size
		}

		if rapid.Bool().Draw(t, "has data") {
			c.want.Data = genValue(2).Draw(t, "data")
			c.doc["meta"] = map[string]any{"data": c.want.Data}
		}

		return c
	})
}

// genNested draws a valid document for nestedSchema.
func genNested() *rapid.Generator[nestedCase] {
	return rapid.Custom(func(t *rapid.T) nestedCase {
		doc := make(map[string]any)
		var want nestedDoc

		if rapid.Bool().Draw(t, "has title") {
			want.Title = genText().Draw(t, "title")
			doc["title"] = want.Title
		}

		if rapid.Bool().Draw(t, "has count") {
			want.Count = rapid.Int64Range(-_maxExactInt, _maxExactInt).Draw(t, "count")
			doc["count"] = want.Count
		}

		if rapid.Bool().Draw(t, "has wait") {
			ms := rapid.Int64Range(0, 1<<40).Draw(t, "wait ms")
			want.Wait = time.Duration(ms) * time.Millisecond
			doc["wait"] = ms
		}

		if rapid.Bool().Draw(t, "has on") {
			want.On = rapid.Bool().Draw(t, "on")
			doc["on"] = want.On
		}

		if rapid.Bool().Draw(t, "has tags") {
			tags := rapid.SliceOfN(rapid.StringMatching(`[a-z0-9]{1,8}`), 0, 4).Draw(t, "tags")
			want.Tags = slices.Clone(tags)
			doc["tags"] = toAnySlice(tags)
		}

		if rapid.Bool().Draw(t, "has item") {
			item := genItem().Draw(t, "item")
			want.Item = &item.want
			doc["item"] = item.doc
		}

		if rapid.Bool().Draw(t, "has items") {
			items := rapid.SliceOfN(genItem(), 0, 3).Draw(t, "items")
			list := make([]any, 0, len(items))

			for _, it := range items {
				want.Items = append(want.Items, it.want)
				list = append(list, it.doc)
			}

			doc["items"] = list
		}

		if rapid.Bool().Draw(t, "has labels") {
			labels := rapid.MapOfN(genText(), genText(), 0, 4).Draw(t, "labels")
			want.Labels = labels
			obj := make(map[string]any, len(labels))

			for k, v := range labels {
				obj[k] = v
			}

			doc["labels"] = obj
		}

		if rapid.Bool().Draw(t, "has raw") {
			want.Raw = genValue(2).Draw(t, "raw")
			doc["raw"] = want.Raw
		}

		return nestedCase{doc: doc, want: want}
	})
}

func toAnySlice[E any](s []E) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}

	return out
}

// fault is an invalid setting injected into a valid document, and the problem it must cause.
type fault struct {
	code string
	key  string
}

// injectFaults replaces up to n top-level settings of doc with invalid values, and adds unknown keys.
// Each fault touches a distinct top-level key, so no fault hides another.
func injectFaults(t *rapid.T, doc map[string]any, n int) []fault {
	type template struct {
		key    string
		inject func(doc map[string]any) fault
	}

	templates := []template{
		{"count", func(doc map[string]any) fault { doc["count"] = "many"; return fault{CodeInvalidValue, "count"} }},
		{"wait", func(doc map[string]any) fault { doc["wait"] = int64(-5); return fault{CodeInvalidValue, "wait"} }},
		{"on", func(doc map[string]any) fault { doc["on"] = "maybe"; return fault{CodeInvalidValue, "on"} }},
		{"tags", func(doc map[string]any) fault {
			doc["tags"] = map[string]any{"a": int64(1)}
			return fault{CodeInvalidValue, "tags"}
		}},
		{"item", func(doc map[string]any) fault {
			doc["item"] = map[string]any{"size": int64(-1)}
			return fault{CodeInvalidValue, "item.size"}
		}},
		{"items", func(doc map[string]any) fault {
			doc["items"] = []any{map[string]any{"name": "ok"}, true}
			return fault{CodeInvalidValue, "items[1]"}
		}},
		{"labels", func(doc map[string]any) fault {
			doc["labels"] = map[string]any{"ok": "x", "X-Num": int64(1)}
			return fault{CodeInvalidValue, "labels.X-Num"}
		}},
		{"raw", func(doc map[string]any) fault {
			doc["raw"] = make(map[string]any)
			doc["rawr"] = int64(1)
			return fault{CodeUnknownKey, "rawr"}
		}},
	}

	picked := rapid.SliceOfNDistinct(rapid.SampledFrom(templates), 1, n, func(tp template) string { return tp.key }).Draw(t, "faults")

	faults := make([]fault, 0, len(picked))
	for _, tp := range picked {
		faults = append(faults, tp.inject(doc))
	}

	// Unknown keys deep inside the document, where suggestions and prefixes matter.
	if rapid.Bool().Draw(t, "unknown nested key") {
		doc["item"] = map[string]any{"nmae": "x"}
		faults = slices.DeleteFunc(faults, func(f fault) bool { return f.key == "item.size" })
		faults = append(faults, fault{CodeUnknownKey, "item.nmae"})
	}

	return faults
}

// String writes the fault the way test failures show it.
func (f fault) String() string {
	return f.code + " " + strconv.Quote(f.key)
}
