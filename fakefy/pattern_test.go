package fakefy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/fakefy"
)

func TestSchema_APatternMakesAStringItMatches(t *testing.T) {
	// Specs constrain codes and identifiers with patterns: a value that breaks one fails the contract it mocks.
	tests := []struct {
		name   string
		schema string
	}{
		{name: "a pattern", schema: `{"type": "string", "pattern": "^[A-Z]{3}-\\d{4}$"}`},
		{name: "a pattern with a minLength", schema: `{"type": "string", "pattern": "^[a-z0-9]+$", "minLength": 8}`},
		{name: "a pattern with an exact length", schema: `{"type": "string", "pattern": "^[0-9]+$", "minLength": 8, "maxLength": 8}`},
		{name: "a pattern of two open repeats with a length", schema: `{"type": "string", "pattern": "^[A-Z]+-[0-9]*$", "minLength": 12, "maxLength": 14}`},
		{name: "groups of a bounded repeat to a length", schema: `{"type": "string", "pattern": "^([a-z]{2,3}-)+[0-9]{2}$", "minLength": 15, "maxLength": 16}`},
		{name: "anchored classes and counts", schema: `{"type": "string", "pattern": "^[A-Z]{2}\\d{3,5}$"}`},
		{name: "alternation and groups", schema: `{"type": "string", "pattern": "^(cat|dog)s?-[0-9a-f]+$"}`},
		{name: "unanchored", schema: `{"type": "string", "pattern": "x+y"}`},
		{name: "a negated class", schema: `{"type": "string", "pattern": "^[^0-9]{4}$"}`},
		{name: "a dot and an open repeat", schema: `{"type": "string", "pattern": "^v.{2,}$"}`},
		{name: "unicode letters", schema: `{"type": "string", "pattern": "^\\p{L}+$"}`},
		{name: "a pattern with lengths", schema: `{"type": "string", "pattern": "^[a-z]+$", "minLength": 3, "maxLength": 4}`},
		{name: "a repeat that can write nothing, to a length", schema: `{"type": "string", "pattern": "^(a?)*b$", "minLength": 3}`},
		{name: "a repeat of an optional item, to a length", schema: `{"type": "string", "pattern": "^([A-Z_]?)+$", "minLength": 17, "maxLength": 24}`},
		{name: "a repeat that never writes, to a length", schema: `{"type": "string", "pattern": "^a(\\b)*", "minLength": 3}`},
		{name: "a class with no printable ASCII", schema: `{"type": "string", "pattern": "^[\\x{100}-\\x{200}]+$"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch, doc := compile(t, tt.schema)

			for seed := range uint64(_seeds) {
				v, err := fakefy.New(fakefy.WithSeed(seed)).Schema(doc)
				require.NoError(t, err)
				requireValid(t, sch, v)
			}
		})
	}
}

func TestSchema_APatternNoStringFitsFailsWithErrUnsatisfiable(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		want   string
	}{
		{name: "a pattern no string of the length matches", schema: `{"type": "string", "pattern": "^a{3}$", "maxLength": 2}`, want: `at the root: pattern "^a{3}$": no string of 0 to 2 characters after 20 tries`},
		{name: "a pattern no long string matches", schema: `{"type": "string", "pattern": "^a{3}$", "minLength": 5}`, want: `at the root: pattern "^a{3}$": no string of at least 5 characters after 20 tries`},
		{name: "a pattern no string matches", schema: `{"type": "string", "pattern": "[^\\x00-\\x{10FFFF}]"}`, want: `at the root: pattern "[^\\x00-\\x{10FFFF}]": no string matches it`},
		{name: "a sequence a part of which no string matches", schema: `{"type": "string", "pattern": "a[^\\x00-\\x{10FFFF}]"}`, want: `at the root: pattern "a[^\\x00-\\x{10FFFF}]": no string matches it`},
		{name: "a repeat of what no string matches", schema: `{"type": "string", "pattern": "([^\\x00-\\x{10FFFF}])+"}`, want: `at the root: pattern "([^\\x00-\\x{10FFFF}])+": no string matches it`},
		{name: "an optional repeat of what no string matches, to a length", schema: `{"type": "string", "pattern": "x[^\\x00-\\x{10FFFF}]*", "minLength": 3}`, want: `at the root: pattern "x[^\\x00-\\x{10FFFF}]*": no string matches it`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, doc := compile(t, tt.schema)

			_, err := fakefy.New(fakefy.WithSeed(1)).Schema(doc)
			require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
			require.EqualError(t, err, "fakefy: no value satisfies the schema: "+tt.want)
		})
	}
}

func TestSchema_ARandomPatternMakesAStringItMatches(t *testing.T) {
	// A spec's pattern is any regular expression its author wrote: every one some string matches must make one.
	rapid.Check(t, func(t *rapid.T) {
		sch, doc := compileMap(t, map[string]any{"type": "string", "pattern": patternGen().Draw(t, "pattern")})

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(doc)
		require.NoError(t, err)
		requireValid(t, sch, v)
	})
}

func TestSchema_ARandomPatternWithLengthsMakesAStringOfThemOrFails(t *testing.T) {
	// Lengths may leave a pattern no string: the generator must then fail with ErrUnsatisfiable, never return a string
	// of another length.
	rapid.Check(t, func(t *rapid.T) {
		lo := rapid.IntRange(0, 30).Draw(t, "minLength")
		schema := map[string]any{"type": "string", "pattern": patternGen().Draw(t, "pattern"), "minLength": lo}
		if rapid.Bool().Draw(t, "bounded") {
			schema["maxLength"] = lo + rapid.IntRange(0, 10).Draw(t, "extra")
		}

		sch, doc := compileMap(t, schema)

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(doc)
		if err != nil {
			require.ErrorIs(t, err, fakefy.ErrUnsatisfiable)
			return
		}

		requireValid(t, sch, v)
	})
}

func TestSchema_APatternOpenAtTheEndMakesAStringOfAnyMinLength(t *testing.T) {
	// JSON Schema patterns search the string, so letters after the match fit a pattern without $: a minLength never
	// leaves it without a string.
	rapid.Check(t, func(t *rapid.T) {
		pattern := strings.TrimSuffix(patternGen().Draw(t, "pattern"), "$")
		sch, doc := compileMap(t, map[string]any{"type": "string", "pattern": pattern, "minLength": rapid.IntRange(0, 40).Draw(t, "minLength")})

		v, err := fakefy.New(fakefy.WithSeed(rapid.Uint64().Draw(t, "seed"))).Schema(doc)
		require.NoError(t, err)
		requireValid(t, sch, v)
	})
}

// Generators

// patternGen draws regular expressions some string matches: literals, classes, groups, alternations and repeats,
// optionally anchored at either end and case-insensitive.
func patternGen() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var b strings.Builder

		if rapid.Bool().Draw(t, "case-insensitive") {
			b.WriteString("(?i)")
		}

		if rapid.Bool().Draw(t, "starts") {
			b.WriteString("^")
		}

		b.WriteString(sequenceGen(2).Draw(t, "sequence"))

		if rapid.Bool().Draw(t, "ends") {
			b.WriteString("$")
		}

		return b.String()
	})
}

// sequenceGen draws one to four atoms, each repeated or not, groups nesting up to depth levels.
func sequenceGen(depth int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		var b strings.Builder

		for i := range rapid.IntRange(1, 4).Draw(t, "atoms") {
			b.WriteString(atomGen(depth).Draw(t, fmt.Sprintf("atom%d", i)))
			b.WriteString(rapid.SampledFrom([]string{"", "", "?", "*", "+", "*?", "{2}", "{0,2}", "{1,}", "{2,3}"}).Draw(t, "repeat"))
		}

		return b.String()
	})
}

// atomGen draws a literal, a class, or, above depth 0, a group of sequences.
func atomGen(depth int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		kinds := []string{"literal", "class"}
		if depth > 0 {
			kinds = append(kinds, "group", "alternation")
		}

		switch rapid.SampledFrom(kinds).Draw(t, "kind") {
		case "literal":
			return rapid.SampledFrom([]string{"a", "Z", "0", "-", "_", " ", "\\.", "\\+", "é", "日"}).Draw(t, "literal")
		case "class":
			return rapid.SampledFrom([]string{
				"[a-z]", "[0-9a-f]", "[^0-9]", "\\d", "\\w", "\\s", "\\D", ".", "[A-Z_]", "\\p{L}", "[[:alpha:]]",
				"[\\x{100}-\\x{200}]",
			}).Draw(t, "class")
		case "group":
			return rapid.SampledFrom([]string{"(", "(?:"}).Draw(t, "group") + sequenceGen(depth-1).Draw(t, "sequence") + ")"
		default:
			alts := make([]string, rapid.IntRange(2, 3).Draw(t, "alternatives"))
			for i := range alts {
				alts[i] = sequenceGen(depth-1).Draw(t, fmt.Sprintf("alternative%d", i))
			}

			return "(" + strings.Join(alts, "|") + ")"
		}
	})
}
