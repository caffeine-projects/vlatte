package match

import (
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

// equalJSONMatcher passes when the JSON of the value equals the expected JSON.
type equalJSONMatcher struct {
	Expected any
	want     any // what the expected JSON means
	opts     jsonOptions
}

// Name implements vlatte.Matcher.
func (m *equalJSONMatcher) Name() string { return "EqualJSON" }

// Match implements vlatte.Matcher.
func (m *equalJSONMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	got, err := jsonOf(v)
	if err == nil && m.opts.same(m.want, got) {
		return success()
	}

	return mismatch(strings.Join([]string{"EqualJSON(", anyconv.Stringify(m.Expected), ") Got: ", anyconv.Stringify(v)}, ""))
}

// jsonOf returns what the JSON of v means: text is read as JSON, as a server reads a request body,
// and any other value is written as JSON first.
func jsonOf(v any) (any, error) {
	if text, ok := value.Text(v); ok {
		return value.DecodeJSONLenient([]byte(text))
	}

	return value.ViaJSON(v)
}

// same reports whether got, a JSON document, equals want, another, as the options loosen it.
func (o jsonOptions) same(want, got any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) < len(w) || len(g) > len(w) && !o.ignoreExtraElements {
			return false
		}

		for k, wv := range w {
			if gv, ok := g[k]; !ok || !o.same(wv, gv) {
				return false
			}
		}

		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) < len(w) || len(g) > len(w) && !o.ignoreExtraElements {
			return false
		}

		if o.ignoreArrayOrder {
			paired, _ := pairUp(len(w), len(g), func(i, j int) (bool, error) { return o.same(w[i], g[j]), nil })
			return paired
		}

		// In order, the expected items are a subsequence of the value's: pairing each with the first item left that
		// it equals leaves the most items for the rest, so this finds one when there is one.
		j := 0
		for _, wv := range w {
			for j < len(g) && !o.same(wv, g[j]) {
				j++
			}

			if j == len(g) {
				return false
			}

			j++
		}

		return true
	default:
		return value.Same(want, got)
	}
}

// Describe implements descriptor.SelfDescriptive. Options are described as an object after the expected JSON, with
// the keys of mock files.
func (m *equalJSONMatcher) Describe() descriptor.Descriptor {
	opts := make(map[string]any, 2)
	if m.opts.ignoreArrayOrder {
		opts["ignore_array_order"] = true
	}

	if m.opts.ignoreExtraElements {
		opts["ignore_extra_elements"] = true
	}

	if len(opts) == 0 {
		return descriptor.MultiArgs("eqj", m.Expected)
	}

	return descriptor.MultiArgs("eqj", m.Expected, opts)
}
