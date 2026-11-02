package fakefy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
