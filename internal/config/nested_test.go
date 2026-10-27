package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// loadNested loads one source into a fresh nestedDoc.
func loadNested(sources ...Source) (nestedDoc, map[string]Value, error) {
	var d nestedDoc
	values, err := nestedSchema().Load(&d, sources)

	return d, values, err
}

// problemSet lists the code and key of each problem of err, sorted, for comparisons that ignore wording.
func problemSet(t require.TestingT, err error) []string {
	cfgErr, ok := errors.AsType[*Error](err)
	require.True(t, ok, "want an *Error, got %v", err)

	set := make([]string, 0, len(cfgErr.Problems))
	for _, p := range cfgErr.Problems {
		set = append(set, fault{p.Code, p.Key}.String())
	}

	slices.Sort(set)

	return set
}

func TestDecode_Nested(t *testing.T) {
	doc := `
title: api
item: {name: first, size: 1, meta: {data: {a: [1, x]}}}
items:
  - {name: a}
  - {size: 2}
labels: {X-Id: "1", lower: x}
raw: [1, 2024-01-01]
`
	d, _, err := loadNested(Bytes("doc", "yaml", []byte(doc)))

	require.NoError(t, err)
	require.Equal(t, nestedDoc{
		Title:  "api",
		Item:   &nestedItem{Name: "first", Size: 1, Data: map[string]any{"a": []any{int64(1), "x"}}},
		Items:  []nestedItem{{Name: "a"}, {Size: 2}},
		Labels: map[string]string{"X-Id": "1", "lower": "x"},
		Raw:    []any{int64(1), "2024-01-01"},
	}, d)
}

func TestDecode_NestedProblems(t *testing.T) {
	// A problem deep in a document names its full key, so it can be found without counting brackets.
	tests := []struct {
		name string
		doc  string
		want []Problem
	}{
		{
			name: "unknown key with a suggestion",
			doc:  "item: {nmae: x}\n",
			want: []Problem{{Code: CodeUnknownKey, Key: "item.nmae", Message: "item.nmae is not a known setting", Hint: "did you mean item.name?"}},
		},
		{
			name: "unknown key lists the keys under the object",
			doc:  "item: {colour: red}\n",
			want: []Problem{{Code: CodeUnknownKey, Key: "item.colour", Message: "item.colour is not a known setting", Hint: "the keys under item are name, size, meta"}},
		},
		{
			name: "unknown key in a group of a list item",
			doc:  "items: [{meta: {dat: 1}}]\n",
			want: []Problem{{
				Code: CodeUnknownKey, Key: "items[0].meta.dat", Message: "items[0].meta.dat is not a known setting",
				Hint: "did you mean items[0].meta.data?",
			}},
		},
		{
			name: "an object that isn't",
			doc:  "item: [1]\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "item", Value: "[1]", Message: "item must be an object, not a list"}},
		},
		{
			name: "a list that isn't",
			doc:  "items: {a: 1}\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "items", Value: `{"a":1}`, Message: "items must be a list, not an object"}},
		},
		{
			name: "a list item that isn't an object",
			doc:  "items: [{name: a}, true]\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "items[1]", Value: "true", Message: "items[1] must be an object, not a bool"}},
		},
		{
			name: "a group that isn't an object in a list item",
			doc:  "items: [{meta: 5}]\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "items[0].meta", Value: "5", Message: "items[0].meta must be an object, not a number", Hint: "its keys are data"}},
		},
		{
			name: "an invalid value in a list item",
			doc:  "items: [{size: -1}]\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "items[0].size", Value: "-1", Message: `items[0].size="-1": must be 0 or more`}},
		},
		{
			name: "a wrong kind in a nested object",
			doc:  "item: {size: big}\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "item.size", Value: "big", Message: "item.size must be an integer, not a string"}},
		},
		{
			name: "a member error names the member as written",
			doc:  "labels: {X-Num: 1}\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "labels.X-Num", Value: "1", Message: `labels.X-Num="1": must be text, not a number`}},
		},
		{
			name: "a dotted key in a nested object",
			doc:  "item: {meta.data: 1}\n",
			want: []Problem{{Code: CodeUnknownKey, Key: "item.meta.data", Message: "item.meta.data is written as a dotted key", Hint: "nest it: meta: {data: ...}"}},
		},
		{
			name: "every problem is reported, sorted by key",
			doc:  "items: [{size: -1}, {nmae: b}]\ncount: x\nlabels: {a: 1}\n",
			want: []Problem{
				{Code: CodeInvalidValue, Key: "count", Value: "x", Message: "count must be an integer, not a string"},
				{Code: CodeInvalidValue, Key: "items[0].size", Value: "-1", Message: `items[0].size="-1": must be 0 or more`},
				{Code: CodeUnknownKey, Key: "items[1].nmae", Message: "items[1].nmae is not a known setting", Hint: "did you mean items[1].name?"},
				{Code: CodeInvalidValue, Key: "labels.a", Value: "1", Message: `labels.a="1": must be text, not a number`},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadNested(Bytes("doc", "yaml", []byte(tc.doc)))

			for i := range tc.want {
				tc.want[i].Origin = "doc"
			}

			require.Equal(t, tc.want, requireProblems(t, err))
		})
	}
}

func TestDecode_NotAnObject(t *testing.T) {
	var it nestedItem
	err := itemSchema().Decode(&it, Value{Key: "x[2]", Origin: "o", Raw: "null", Any: nil})

	require.Equal(t, []Problem{{Code: CodeInvalidValue, Origin: "o", Key: "x[2]", Value: "null", Message: "x[2] must be an object, not null"}},
		requireProblems(t, err))
}

func TestProblemsOf(t *testing.T) {
	// A caller that adds problems of its own to a decoder's merges them through ProblemsOf, and loses none: an
	// *Error's problems are its own, wrapped or not, and any other error is a problem of the value.
	v := Value{Key: "m.body", Origin: "o", Raw: "x", Any: "x"}
	nested := &Error{Problems: []Problem{{Code: CodeConflict, Origin: "o", Key: "m.a"}}}

	require.Nil(t, ProblemsOf(v, nil))
	require.Equal(t, nested.Problems, ProblemsOf(v, nested))
	require.Equal(t, nested.Problems, ProblemsOf(v, fmt.Errorf("wrapped: %w", nested)))
	require.Equal(t, []Problem{{Code: CodeInvalidValue, Origin: "o", Key: "m.body", Value: "x", Message: `m.body="x": boom`}},
		ProblemsOf(v, errors.New("boom")))
}

func TestPropNested_FormatsLoadAlike(t *testing.T) {
	// C5: a document written as JSON and as YAML loads into the same struct and values; only origins differ.
	rapid.Check(t, func(t *rapid.T) {
		c := genNested().Draw(t, "doc")

		loaded := make([]nestedDoc, 0, 2)
		allValues := make([]map[string]Value, 0, 2)

		for _, format := range []string{"json", "yaml"} {
			d, values, err := loadNested(Bytes("doc."+format, format, encodeAs(t, c.doc, format)))
			require.NoError(t, err, format)

			for k, v := range values {
				require.Equal(t, "doc."+format, v.Origin, k)
				v.Origin = ""
				values[k] = v
			}

			loaded = append(loaded, d)
			allValues = append(allValues, values)
		}

		require.Equal(t, c.want, loaded[0], "json")
		require.Equal(t, c.want, loaded[1], "yaml")
		require.Equal(t, allValues[0], allValues[1])
	})
}

func TestPropNested_EveryProblemIsReported(t *testing.T) {
	// C6: each injected fault is reported exactly once, nothing else is, and valid settings still apply.
	rapid.Check(t, func(t *rapid.T) {
		c := genNested().Draw(t, "doc")
		faults := injectFaults(t, c.doc, 4)
		format := rapid.SampledFrom([]string{"json", "yaml"}).Draw(t, "format")

		d, _, err := loadNested(Bytes("doc", format, encodeAs(t, c.doc, format)))

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			want = append(want, f.String())
		}

		slices.Sort(want)
		require.Equal(t, want, problemSet(t, err))
		require.Equal(t, c.want.Title, d.Title, "a valid setting next to invalid ones still applies")

		cfgErr, _ := errors.AsType[*Error](err)
		require.True(t, slices.IsSortedFunc(cfgErr.Problems, func(a, b Problem) int {
			return strings.Compare(a.Origin+"\x00"+a.Key, b.Origin+"\x00"+b.Key)
		}), "problems are sorted by origin and key")
	})
}

func TestPropNested_KeysAreUnderTheParent(t *testing.T) {
	// C7: whatever object Decode gets, every problem is keyed under the parent key, says its key,
	// and every suggestion is a real key under the parent.
	keys := itemSchema().Keys()
	member := rapid.SampledFrom([]string{"name", "size", "meta", "nmae", "siz", "metadata", "meta.data", "x"})

	rapid.Check(t, func(t *rapid.T) {
		parent := rapid.StringMatching(`[a-z]{1,5}(\.[a-z]{1,5}|\[[0-9]\]){0,2}`).Draw(t, "parent")
		obj := rapid.MapOfN(member, genValue(2), 0, 5).Draw(t, "object")

		var it nestedItem
		err := itemSchema().Decode(&it, Value{Key: parent, Origin: "o", Raw: rawText(obj), Any: obj})
		if err == nil {
			return
		}

		cfgErr, ok := errors.AsType[*Error](err)
		require.True(t, ok)

		for _, p := range cfgErr.Problems {
			require.Equal(t, "o", p.Origin)
			require.True(t, p.Key == parent || strings.HasPrefix(p.Key, parent+".") || strings.HasPrefix(p.Key, parent+"["), p.Key)
			require.Contains(t, p.Message, p.Key)

			if near, ok := strings.CutPrefix(p.Hint, "did you mean "); ok {
				near = strings.TrimSuffix(near, "?")
				rel, under := strings.CutPrefix(near, parent+".")
				require.True(t, under, near)
				require.Contains(t, keys, rel)
			}
		}
	})
}

func TestPropNested_ItemsAndMembersKeyEveryFailure(t *testing.T) {
	// C7: Items and Members report exactly the elements fn rejects, keyed by index or by name as written.
	rapid.Check(t, func(t *rapid.T) {
		list := rapid.SliceOfN(genValue(1), 0, 6).Draw(t, "list")
		obj := rapid.MapOfN(genText(), genValue(1), 0, 6).Draw(t, "object")
		failIndex := rapid.SliceOf(rapid.IntRange(0, 5)).Draw(t, "failing indexes")
		failName := rapid.SliceOf(rapid.SampledFrom(append(slices.Collect(maps.Keys(obj)), "absent"))).Draw(t, "failing names")

		var want []string
		err := Items(Value{Key: "l", Origin: "o", Any: list}, func(item Value) error {
			i, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(item.Key, "l["), "]"))
			require.Equal(t, list[i], item.Any)

			if slices.Contains(failIndex, i) {
				want = append(want, fault{CodeInvalidValue, item.Key}.String())
				return errors.New("rejected")
			}

			return nil
		})
		requireSameSet(t, want, err)

		want = nil
		err = Members(Value{Key: "m", Origin: "o", Any: obj}, func(name string, m Value) error {
			require.Equal(t, "m."+name, m.Key)
			require.Equal(t, obj[name], m.Any)

			if slices.Contains(failName, name) {
				want = append(want, fault{CodeInvalidValue, m.Key}.String())
				return errors.New("rejected")
			}

			return nil
		})
		requireSameSet(t, want, err)
	})
}

func requireSameSet(t require.TestingT, want []string, err error) {
	if len(want) == 0 {
		require.NoError(t, err)
		return
	}

	slices.Sort(want)
	require.Equal(t, want, problemSet(t, err))
}

func TestPropNested_CoercionIsTheSameInEverySource(t *testing.T) {
	// C8: a value means the same in a file, an env var and a flag, when the file writes it with its type:
	// numbers and bools bare, text in quotes.
	outer := t

	load := func(t *rapid.T, key, fileValue, text string) []nestedDoc {
		env := "NEST_" + strings.ToUpper(key)
		outer.Setenv(env, text)
		defer outer.Setenv(env, "")

		docs := make([]nestedDoc, 0, 4)
		for _, src := range []Source{
			Bytes("yaml", "yaml", fmt.Appendf(nil, "%s: %s\n", key, fileValue)),
			Bytes("json", "json", fmt.Appendf(nil, `{%q: %s}`, key, fileValue)),
			Env("NEST"),
			Args([]string{"--" + key + "=" + text}),
		} {
			d, _, err := loadNested(src)
			require.NoError(t, err)

			docs = append(docs, d)
		}

		return docs
	}

	rapid.Check(t, func(t *rapid.T) {
		ms := rapid.Int64Range(0, 1<<40).Draw(t, "ms")
		for _, d := range load(t, "wait", strconv.FormatInt(ms, 10), strconv.FormatInt(ms, 10)) {
			require.Equal(t, time.Duration(ms)*time.Millisecond, d.Wait)
		}

		dur := time.Duration(rapid.Int64Range(0, 1<<50).Draw(t, "ns"))
		for _, d := range load(t, "wait", strconv.Quote(dur.String()), dur.String()) {
			require.Equal(t, dur, d.Wait, dur.String())
		}

		n := rapid.Int64Range(-_maxExactInt, _maxExactInt).Draw(t, "count")
		for _, d := range load(t, "count", strconv.FormatInt(n, 10), strconv.FormatInt(n, 10)) {
			require.Equal(t, n, d.Count)
		}

		flag := rapid.SampledFrom([]string{"true", "false", "1", "0", "t", "F", "TRUE"}).Draw(t, "on")
		want, _ := strconv.ParseBool(flag)

		for _, d := range load(t, "on", strconv.Quote(flag), flag) {
			require.Equal(t, want, d.On, "a bool reads as strconv.ParseBool reads it, in every source")
		}

		scalar := rapid.SampledFrom([]string{"12", "-3", "true", "false", "1.5"}).Draw(t, "title")
		for _, d := range load(t, "title", strconv.Quote(scalar), scalar) {
			require.Equal(t, scalar, d.Title, "text in quotes is a string in every source")
		}

		// Unquoted, a document holds a number or a bool, which a string setting doesn't take.
		for _, src := range []Source{
			Bytes("yaml", "yaml", fmt.Appendf(nil, "title: %s\n", scalar)),
			Bytes("json", "json", fmt.Appendf(nil, `{"title": %s}`, scalar)),
		} {
			_, _, err := loadNested(src)

			problems := requireProblems(t, err)
			require.Len(t, problems, 1)
			require.Regexp(t, `^title must be a string, not a (number|bool)$`, problems[0].Message)
			require.Equal(t, "write it in quotes: "+strconv.Quote(scalar), problems[0].Hint)
		}
	})
}

func TestPropNested_InvalidDurationsInEverySource(t *testing.T) {
	// C8: negative and fractional numbers are never durations, wherever they come from.
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.Int64Range(1, 1<<40).Draw(t, "n")
		bad := rapid.SampledFrom([]string{"-" + strconv.FormatInt(n, 10), strconv.FormatInt(n, 10) + ".5"}).Draw(t, "value")

		for _, src := range []Source{
			Bytes("yaml", "yaml", fmt.Appendf(nil, "wait: %s\n", bad)),
			Bytes("json", "json", fmt.Appendf(nil, `{"wait": %s}`, bad)),
			Args([]string{"--wait=" + bad}),
		} {
			_, _, err := loadNested(src)
			require.Equal(t, []string{fault{CodeInvalidValue, "wait"}.String()}, problemSet(t, err), bad)
		}
	})
}

func TestPropNested_Deterministic(t *testing.T) {
	// C9: the same input gives the same result and the same error text, every time.
	rapid.Check(t, func(t *rapid.T) {
		c := genNested().Draw(t, "doc")
		if rapid.Bool().Draw(t, "with faults") {
			injectFaults(t, c.doc, 4)
		}

		data := encodeAs(t, c.doc, "yaml")

		d1, v1, err1 := loadNested(Bytes("doc", "yaml", data))
		d2, v2, err2 := loadNested(Bytes("doc", "yaml", data))

		require.Equal(t, d1, d2)
		require.Equal(t, v1, v2)

		if err1 != nil || err2 != nil {
			require.EqualError(t, err2, err1.Error())
		}
	})
}

// Generators

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
