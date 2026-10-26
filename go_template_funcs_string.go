package vlatte

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// _ellipsis ends a string truncate cuts.
const _ellipsis = "..."

// title uppercases the first letter of each word of s, words being separated by white space. Other letters are
// unchanged.
func title(s string) string {
	var b strings.Builder

	b.Grow(len(s))

	start := true
	for _, r := range s {
		if start {
			r = unicode.ToUpper(r)
		}

		start = unicode.IsSpace(r)
		b.WriteRune(r)
	}

	return b.String()
}

// upperFirst uppercases the first letter of s.
func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return s
	}

	return string(unicode.ToUpper(r)) + s[size:]
}

// truncate cuts s to maxLen runes, the last three being "...". s fits as it is when it is short enough.
func truncate(maxLen int, s string) (string, error) {
	if maxLen <= len(_ellipsis) {
		return "", fmt.Errorf("length %d is not more than %d", maxLen, len(_ellipsis))
	}

	if utf8.RuneCountInString(s) <= maxLen {
		return s, nil
	}

	return string([]rune(s)[:maxLen-len(_ellipsis)]) + _ellipsis, nil
}

// replaceAll replaces each old in s with replacement. Its arguments are in the order of a pipeline, s last.
func replaceAll(old, replacement, s string) string {
	return strings.ReplaceAll(s, old, replacement)
}

// split splits s around each sep.
func split(sep, s string) []string {
	return strings.Split(s, sep)
}

// join joins the items of a list with sep, each written as print writes it.
func join(sep string, list any) (string, error) {
	items, err := listItems(list)
	if err != nil {
		return "", err
	}

	texts := make([]string, len(items))
	for i, item := range items {
		texts[i] = fmt.Sprint(item)
	}

	return strings.Join(texts, sep), nil
}

// substring returns the runes of s from start to end. An end below 0 is the end of s.
func substring(start, end int, s string) (string, error) {
	runes := []rune(s)
	if end < 0 {
		end = len(runes)
	}

	if start < 0 || start > end || end > len(runes) {
		return "", fmt.Errorf("range [%d:%d] is out of the %d runes of the string", start, end, len(runes))
	}

	return string(runes[start:end]), nil
}

// slug lowercases s and joins its runs of letters and digits with "-".
func slug(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	return strings.Join(words, "-")
}

// regexpFind returns the first match of pattern in s, or "" when there is none.
func regexpFind(pattern, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}

	return re.FindString(s), nil
}

// regexpSubmatch returns the first match of pattern in s and its groups, or nil when there is none.
func regexpSubmatch(pattern, s string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	return re.FindStringSubmatch(s), nil
}

// regexpReplaceAll replaces each match of pattern in s with replacement, in which $1 and ${name} are groups.
func regexpReplaceAll(pattern, replacement, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}

	return re.ReplaceAllString(s, replacement), nil
}

// regexpMatch reports whether s holds a match of pattern.
func regexpMatch(pattern, s string) (bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, err
	}

	return re.MatchString(s), nil
}

// contains reports whether collection holds item: a string holds a substring, a list an item equal to it, and a map
// a key equal to it. Numbers are equal by value, whatever their types, so a JSON number holds an integer literal.
func contains(item, collection any) (bool, error) {
	if collection == nil {
		return false, nil
	}

	if s, ok := collection.(string); ok {
		sub, ok := item.(string)
		if !ok {
			return false, fmt.Errorf("a string holds strings, not %T", item)
		}

		return strings.Contains(s, sub), nil
	}

	v := reflect.ValueOf(collection)

	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if sameValue(item, v.Index(i).Interface()) {
				return true, nil
			}
		}

		return false, nil
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if sameValue(item, k.Interface()) {
				return true, nil
			}
		}

		return false, nil
	default:
		return false, fmt.Errorf("%T is not a string, a list or a map", collection)
	}
}

// _emptyKinds are the kinds whose values are empty when their length is 0.
var _emptyKinds = []reflect.Kind{reflect.String, reflect.Slice, reflect.Array, reflect.Map}

// defaultValue returns v, or fallback when v is empty: nil, "", or a list or map without items. 0 and false are not
// empty, so a real zero stays.
func defaultValue(fallback, v any) any {
	if v == nil {
		return fallback
	}

	rv := reflect.ValueOf(v)

	switch {
	case rv.Kind() == reflect.Pointer && rv.IsNil():
		return fallback
	case slices.Contains(_emptyKinds, rv.Kind()) && rv.Len() == 0:
		return fallback
	default:
		return v
	}
}
