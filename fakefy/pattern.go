package fakefy

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// _printableLo to _printableHi are the runes a character class gives first, printable ASCII, when it holds some.
const (
	_printableLo = 0x20
	_printableHi = 0x7e
)

// errNoMatch is the error of a pattern no string matches, such as [^\x00-\x{10FFFF}].
var errNoMatch = errors.New("no string matches it")

// pattern returns a string every one of res matches, with lo to hi runes when bounded, or at least lo. It writes
// strings the first of res matches, and keeps the first the others match too. JSON Schema patterns search the string,
// so a string a whole pattern matches does, and one too short may take letters after it, or before, where the
// patterns don't anchor it.
func (f *Faker) pattern(res []*regexp.Regexp, lo, hi int, bounded bool) (string, error) {
	// res[0] compiled, so it parses.
	tree, _ := syntax.Parse(res[0].String(), syntax.Perl)
	tree = tree.Simplify()

	for range _maxTries {
		// With lengths, repeats write up to a length of lo, or a little more, and no further: no length is -1.
		target := -1
		if lo > 0 || bounded {
			top := lo + 2
			if bounded {
				top = min(hi, top)
			}

			target = lo + f.rnd.IntN(max(top-lo, 0)+1)
		}

		var b strings.Builder
		if err := f.regexp(tree, &b, 0, target); err != nil {
			return "", err
		}

		s := b.String()
		n := utf8.RuneCountInString(s)

		if n > lo {
			if (!bounded || n <= hi) && matchAll(res, s) {
				return s, nil
			}

			continue
		}

		pad := f.letters(lo - n)
		for _, v := range []string{s + pad, pad + s} {
			if matchAll(res, v) {
				return v, nil
			}
		}
	}

	return "", fmt.Errorf("no string of %s after %d tries", lengths(lo, hi, bounded), _maxTries)
}

// letters returns n random lowercase letters.
func (f *Faker) letters(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + f.rnd.IntN(26))
	}

	return string(b)
}

// matchAll reports whether every one of res matches s.
func matchAll(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if !re.MatchString(s) {
			return false
		}
	}

	return true
}

// regexp writes a string that re matches to b. rest is the fewest runes the pattern writes after re. With a target
// length, repeats repeat until b and rest reach it; without one (-1), those without an upper bound repeat up to twice
// more than their minimum.
func (f *Faker) regexp(re *syntax.Regexp, b *strings.Builder, rest, target int) error {
	switch re.Op {
	case syntax.OpLiteral:
		b.WriteString(string(re.Rune))
	case syntax.OpCharClass, syntax.OpNoMatch:
		// A class with no ranges, as OpNoMatch has, fails.
		r, err := f.class(re.Rune)
		if err != nil {
			return err
		}

		b.WriteRune(r)
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		b.WriteByte(byte('a' + f.rnd.IntN(26)))
	case syntax.OpCapture:
		return f.regexp(re.Sub[0], b, rest, target)
	case syntax.OpConcat:
		for i, sub := range re.Sub {
			after := rest
			for _, next := range re.Sub[i+1:] {
				after += minLen(next)
			}

			if err := f.regexp(sub, b, after, target); err != nil {
				return err
			}
		}
	case syntax.OpAlternate:
		return f.regexp(re.Sub[f.rnd.IntN(len(re.Sub))], b, rest, target)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		return f.repeat(re, b, rest, target)
	default:
		// Anchors, word boundaries and the empty match write nothing.
	}

	return nil
}

// repeat writes the repeats of re, a *, +, ? or {n,m}, to b, as [Faker.regexp] has them.
func (f *Faker) repeat(re *syntax.Regexp, b *strings.Builder, rest, target int) error {
	lo, hi := re.Min, re.Max

	switch re.Op {
	case syntax.OpStar:
		lo, hi = 0, -1
	case syntax.OpPlus:
		lo, hi = 1, -1
	case syntax.OpQuest:
		lo, hi = 0, 1
	}

	// A repeat with an upper bound repeats a random number of times; one without, with a target length, repeats
	// until the target, and otherwise up to twice more than its minimum.
	n := lo
	if hi >= 0 || target < 0 {
		top := hi
		if top < 0 || top > lo+2 {
			top = lo + 2
		}

		n = lo + f.rnd.IntN(top-lo+1)
	}

	for range n {
		if err := f.regexp(re.Sub[0], b, rest, target); err != nil {
			return err
		}
	}

	if hi >= 0 || target < 0 {
		return nil
	}

	// A repeat past n may write nothing by chance, as [A-Z]? does, so only _maxTries in a row that write nothing end
	// it: one of (^)* never writes.
	for length, empty := utf8.RuneCountInString(b.String()), 0; length+rest < target && empty < _maxTries; {
		before := b.Len()
		if err := f.regexp(re.Sub[0], b, rest, target); err != nil {
			return err
		}

		if b.Len() == before {
			empty++
			continue
		}

		empty = 0
		length += utf8.RuneCountInString(b.String()[before:])
	}

	return nil
}

// minLen returns the fewest runes a string re, simplified, matches has: Simplify writes {n,m} repeats out.
func minLen(re *syntax.Regexp) int {
	switch re.Op {
	case syntax.OpLiteral:
		return len(re.Rune)
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return 1
	case syntax.OpCapture, syntax.OpPlus:
		return minLen(re.Sub[0])
	case syntax.OpConcat:
		n := 0
		for _, sub := range re.Sub {
			n += minLen(sub)
		}

		return n
	case syntax.OpAlternate:
		n := math.MaxInt
		for _, sub := range re.Sub {
			n = min(n, minLen(sub))
		}

		return n
	default:
		return 0
	}
}

// class returns a rune of ranges, pairs of the lowest and highest rune of each range: a printable ASCII one when
// they hold some.
func (f *Faker) class(ranges []rune) (rune, error) {
	var printable [][2]rune

	for i := 0; i+1 < len(ranges); i += 2 {
		lo, hi := max(ranges[i], _printableLo), min(ranges[i+1], _printableHi)
		if lo <= hi {
			printable = append(printable, [2]rune{lo, hi})
		}
	}

	if len(printable) > 0 {
		r := printable[f.rnd.IntN(len(printable))]
		return r[0] + f.rnd.Int32N(r[1]-r[0]+1), nil
	}

	if len(ranges) < 2 {
		return 0, errNoMatch
	}

	i := 2 * f.rnd.IntN(len(ranges)/2)

	return ranges[i] + f.rnd.Int32N(ranges[i+1]-ranges[i]+1), nil
}
