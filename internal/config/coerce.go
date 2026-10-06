package config

import (
	json "encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte/internal/value"
)

// _maxMilliseconds is the most milliseconds a time.Duration holds.
const _maxMilliseconds = math.MaxInt64 / int64(time.Millisecond)

// coerce converts the raw value of e to the kind of f. key is the key users see, such as "response.status".
// A value decoded from a document must already have the type of its kind, so nothing is converted,
// except that a bool reads everywhere the way strconv.ParseBool reads text.
// Text from env and args is parsed: a number in the JSON grammar, a list as comma-separated items.
func coerce(f *field, e entry, key string) (Value, []Problem) {
	v := Value{Key: key, Origin: e.origin, Raw: rawText(e.raw)}
	r := reading{f: f, e: e, key: key}

	var p *Problem

	switch f.kind {
	case String:
		v.Str, p = r.str()
	case Bool:
		v.Bool, p = r.boolean()
	case Int:
		if len(f.choices) > 0 {
			v.Int, p = r.choice()
		} else {
			v.Int, p = r.integer()
		}
	case Duration:
		v.Dur, p = r.duration()
	case List:
		var problems []Problem
		if v.List, problems = r.list(); len(problems) > 0 {
			return Value{}, problems
		}
	case Any:
		v.Any = e.raw
	}

	if p != nil {
		return Value{}, []Problem{*p}
	}

	return v, nil
}

// reading is a raw value being converted to the kind of its field.
type reading struct {
	f   *field
	e   entry
	key string
}

func (r reading) str() (string, *Problem) {
	if s, ok := r.e.raw.(string); ok {
		return s, nil
	}

	return "", r.wrongType()
}

func (r reading) boolean() (bool, *Problem) {
	if b, ok := value.Bool(r.e.raw); ok {
		return b, nil
	}

	switch r.e.raw.(type) {
	case string, int64, float64:
		return false, r.wrongValue("true or false", "")
	default:
		return false, r.wrongType()
	}
}

func (r reading) integer() (int64, *Problem) {
	n := r.e.raw
	if s, ok := n.(string); ok && r.e.text {
		if n, ok = value.ParseNumber(s); !ok {
			return 0, r.wrongValue("an integer", "")
		}
	}

	switch x := n.(type) {
	case int64:
		return x, nil
	case float64:
		hint := ""
		if x == math.Trunc(x) {
			hint = fmt.Sprintf("integers go from %d to %d", math.MinInt64, math.MaxInt64)
		}

		return 0, r.wrongValue("an integer", hint)
	default:
		return 0, r.wrongType()
	}
}

// choice reads the name of a choice, in any case.
func (r reading) choice() (int64, *Problem) {
	s, ok := r.e.raw.(string)
	if !ok {
		p := r.wrongType()
		if n, isInt := r.e.raw.(int64); isInt {
			p.Hint = r.nameOf(n)
		}

		return 0, p
	}

	names := make([]string, 0, len(r.f.choices))
	for _, choice := range r.f.choices {
		if strings.EqualFold(s, choice.Name) {
			return choice.Value, nil
		}

		names = append(names, choice.Name)
	}

	hint := ""
	if n, isNumber := value.ParseNumber(s); isNumber {
		if i, isInt := n.(int64); isInt {
			hint = r.nameOf(i)
		}
	} else if near := suggest(strings.ToLower(s), names); near != "" {
		hint = "did you mean " + near + "?"
	}

	return 0, r.wrongValue(want(r.f), hint)
}

// nameOf is the hint to write the name of the choice whose value n is, if there is one.
func (r reading) nameOf(n int64) string {
	for _, choice := range r.f.choices {
		if choice.Value == n {
			return "write the name: " + choice.Name
		}
	}

	return ""
}

func (r reading) duration() (time.Duration, *Problem) {
	n := r.e.raw
	if s, ok := n.(string); ok && r.e.text {
		// Text that is a number is milliseconds; any other text is a Go duration.
		if ms, isNumber := value.ParseNumber(s); isNumber {
			n = ms
		}
	}

	switch x := n.(type) {
	case int64:
		switch {
		case x < 0:
			return 0, r.wrongValue("0 or more", "")
		case x > _maxMilliseconds:
			return 0, r.wrongValue(fmt.Sprintf("at most %d milliseconds", _maxMilliseconds), "")
		default:
			return time.Duration(x) * time.Millisecond, nil
		}
	case float64:
		return 0, r.wrongValue("whole milliseconds or a Go duration", `such as "1.5ms"`)
	case string:
		return r.goDuration(x)
	default:
		return 0, r.wrongType()
	}
}

// goDuration reads an unsigned Go duration, such as 1.5s.
func (r reading) goDuration(s string) (time.Duration, *Problem) {
	if strings.HasPrefix(s, "-") {
		return 0, r.wrongValue("0 or more", "")
	}

	d, err := time.ParseDuration(s)
	if err == nil && !strings.HasPrefix(s, "+") {
		return d, nil
	}

	hint := "such as 1500 or 1.5s"
	if n, isNumber := value.ParseNumber(s); isNumber && !r.e.text {
		if _, isInt := n.(int64); isInt {
			hint = fmt.Sprintf("write %s without quotes for milliseconds, or add a unit, such as %q", s, s+"ms")
		}
	}

	return 0, r.wrongValue("milliseconds or a Go duration", hint)
}

// list reads a list of strings from a document, or comma-separated items from text. An item that isn't a string
// is a problem of its own, keyed by its index.
func (r reading) list() ([]string, []Problem) {
	if s, ok := r.e.raw.(string); ok && r.e.text {
		items := []string{}
		for item := range strings.SplitSeq(s, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}

		return items, nil
	}

	raw, ok := r.e.raw.([]any)
	if !ok {
		return nil, []Problem{*r.wrongType()}
	}

	items := make([]string, 0, len(raw))

	var problems []Problem
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			problems = append(problems, mismatch(fmt.Sprintf("%s[%d]", r.key, i), r.e.origin, item, "a string", String))
			continue
		}

		items = append(items, s)
	}

	return items, problems
}

// wrongType is the problem of a value of a type the field doesn't take.
func (r reading) wrongType() *Problem {
	p := mismatch(r.key, r.e.origin, r.e.raw, want(r.f), r.f.kind)
	return &p
}

// wrongValue is the problem of a value of a type the field takes, but not of a value it takes:
// "<key> must be <want>, not <the value>".
func (r reading) wrongValue(want, hint string) *Problem {
	return &Problem{
		Code:    CodeInvalidValue,
		Origin:  r.e.origin,
		Key:     r.key,
		Value:   display(rawText(r.e.raw)),
		Message: fmt.Sprintf("%s must be %s, not %s", r.key, want, shown(r.e.raw)),
		Hint:    hint,
	}
}

// want says what a value of the kind of f must be.
func want(f *field) string {
	switch f.kind {
	case String:
		return "a string"
	case Bool:
		return "true or false"
	case Int:
		if len(f.choices) > 0 {
			return "one of " + orList(choiceNames(f.choices))
		}

		return "an integer"
	case Duration:
		return "milliseconds or a Go duration"
	case List:
		return "a list of strings"
	default:
		return "a value"
	}
}

// mismatch is the problem of raw, the value of key, of a type key doesn't take: "<key> must be <want>, not <a list>".
// takes lists the kinds key takes, which pick the hint.
func mismatch(key, origin string, raw any, want string, takes ...Kind) Problem {
	return Problem{
		Code:    CodeInvalidValue,
		Origin:  origin,
		Key:     key,
		Value:   display(rawText(raw)),
		Message: fmt.Sprintf("%s must be %s, not %s", key, want, Describe(raw)),
		Hint:    typeHint(raw, takes),
	}
}

// typeHint suggests how to write raw as a value of one of the kinds taken, for the usual slips in YAML:
// a number or a bool where a string is taken, an integer in quotes, and text where a list is taken.
func typeHint(raw any, takes []Kind) string {
	switch x := raw.(type) {
	case bool, int64, float64:
		if slices.Contains(takes, String) {
			return fmt.Sprintf("write it in quotes: %q", rawText(x))
		}
	case string:
		if n, isNumber := value.ParseNumber(x); isNumber && slices.Contains(takes, Int) {
			if _, isInt := n.(int64); isInt {
				return "write it without quotes: " + x
			}
		}

		if slices.Contains(takes, List) {
			return "write a list: " + listOf(x)
		}
	}

	return ""
}

// listOf writes comma-separated text as a list, as in ["a", "b"].
func listOf(s string) string {
	var items []string
	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, strconv.Quote(item))
		}
	}

	return "[" + strings.Join(items, ", ") + "]"
}

// choiceNames lists the names of choices, in order.
func choiceNames(choices []Choice) []string {
	names := make([]string, 0, len(choices))
	for _, choice := range choices {
		names = append(names, choice.Name)
	}

	return names
}

// orList joins items as in "a, b or c".
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// shown writes a value the way it was given, quoting text, for messages.
func shown(raw any) string {
	if s, ok := raw.(string); ok {
		return strconv.Quote(display(s))
	}

	return display(rawText(raw))
}

// scalarText formats a scalar.
func scalarText(raw any) (string, bool) {
	switch r := raw.(type) {
	case string:
		return r, true
	case bool:
		return strconv.FormatBool(r), true
	case int64:
		return strconv.FormatInt(r, 10), true
	case float64:
		return strconv.FormatFloat(r, 'f', -1, 64), true
	default:
		return "", false
	}
}

// rawText writes a value the way it was given, for messages.
func rawText(raw any) string {
	if s, ok := scalarText(raw); ok {
		return s
	}

	if raw == nil {
		return "null"
	}

	b, err := json.Marshal(raw, json.Deterministic(true))
	if err != nil {
		return fmt.Sprint(raw)
	}

	return string(b)
}

// Describe names the type of a decoded value for messages, such as "a list" or "an object".
func Describe(raw any) string {
	switch raw.(type) {
	case nil:
		return "null"
	case bool:
		return "a bool"
	case string:
		return "a string"
	case int, int64, uint64, float64:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	case map[any]any:
		return "an object with non-string keys"
	default:
		return fmt.Sprintf("a %T", raw)
	}
}
