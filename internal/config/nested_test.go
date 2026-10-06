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
			want: []Problem{{Code: CodeInvalidValue, Key: "items[0].meta", Message: "items[0].meta must be an object, not a number", Hint: "its keys are data"}},
		},
		{
			name: "an invalid value in a list item",
			doc:  "items: [{size: -1}]\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "items[0].size", Value: "-1", Message: `items[0].size="-1": must be 0 or more`}},
		},
		{
			name: "a wrong kind in a nested object",
			doc:  "item: {size: big}\n",
			want: []Problem{{Code: CodeInvalidValue, Key: "item.size", Value: "big", Message: `item.size="big" is not an integer`}},
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
				{Code: CodeInvalidValue, Key: "count", Value: "x", Message: `count="x" is not an integer`},
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
	// C8: a value means the same in a file, an env var and a flag.
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

		scalar := rapid.SampledFrom([]string{"12", "-3", "true", "false", "1.5"}).Draw(t, "title")
		for _, d := range load(t, "title", scalar, scalar) {
			require.Equal(t, scalar, d.Title, "a scalar where text is expected is its text")
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
