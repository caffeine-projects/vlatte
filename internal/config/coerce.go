package config

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// coerce converts the raw value of e to the kind of f.
// Text from env and args is parsed; values decoded from files are checked, and their scalars are lenient.
func coerce(f *field, e entry) (Value, *Problem) {
	v := Value{Key: f.key, Origin: e.origin, Raw: rawText(e.raw)}
	shown := display(v.Raw)

	invalid := func(message, hint string) (Value, *Problem) {
		return Value{}, &Problem{
			Code:    CodeInvalidValue,
			Origin:  e.origin,
			Key:     f.key,
			Value:   shown,
			Message: message,
			Hint:    hint,
		}
	}

	switch f.kind {
	case String:
		s, ok := scalarText(e.raw)
		if !ok {
			return invalid(fmt.Sprintf("%s must be a string, not %s", f.key, describe(e.raw)), "")
		}

		v.Str = s

	case Bool:
		switch raw := e.raw.(type) {
		case bool:
			v.Bool = raw
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return invalid(fmt.Sprintf("%s=%q is not a bool", f.key, shown), "use true or false")
			}

			v.Bool = b
		default:
			return invalid(fmt.Sprintf("%s must be true or false, not %s", f.key, describe(e.raw)), "")
		}

	case Int:
		n, ok := integer(e.raw)
		if !ok && len(f.choices) > 0 {
			n, ok = choiceValue(f.choices, e.raw)
		}

		switch {
		case ok && (len(f.choices) == 0 || hasChoice(f.choices, n)):
			v.Int = n
		case len(f.choices) > 0:
			return invalid(fmt.Sprintf("%s=%q is not an allowed value", f.key, shown), "use one of: "+choiceList(f.choices))
		default:
			return invalid(fmt.Sprintf("%s=%q is not an integer", f.key, shown), "")
		}

	case Duration:
		d, ok := duration(e.raw)
		switch {
		case !ok:
			return invalid(fmt.Sprintf("%s=%q is not a duration", f.key, shown), "use milliseconds (1500) or a Go duration (1.5s)")
		case d < 0:
			return invalid(fmt.Sprintf("%s=%q is negative", f.key, shown), "use 0 or more")
		}

		v.Dur = d

	case List:
		l, ok := list(e.raw)
		if !ok {
			return invalid(fmt.Sprintf("%s must be a list or a comma-separated string, not %s", f.key, describe(e.raw)), "")
		}

		v.List = l
	}

	return v, nil
}

// scalarText formats a scalar. Files may hold a number or a bool where text is expected.
func scalarText(raw any) (string, bool) {
	switch r := raw.(type) {
	case string:
		return r, true
	case bool:
		return strconv.FormatBool(r), true
	case int:
		return strconv.Itoa(r), true
	case int64:
		return strconv.FormatInt(r, 10), true
	case uint64:
		return strconv.FormatUint(r, 10), true
	case float64:
		return strconv.FormatFloat(r, 'f', -1, 64), true
	default:
		return "", false
	}
}

func integer(raw any) (int64, bool) {
	switch r := raw.(type) {
	case int:
		return int64(r), true
	case int64:
		return r, true
	case uint64:
		return int64(r), r <= math.MaxInt64
	case float64:
		// JSON numbers decode as float64; they are exact up to 2^53.
		if r != math.Trunc(r) || math.Abs(r) > 1<<53 {
			return 0, false
		}

		return int64(r), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(r), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func choiceValue(choices []Choice, raw any) (int64, bool) {
	s, ok := raw.(string)
	if !ok {
		return 0, false
	}

	for _, choice := range choices {
		if strings.EqualFold(strings.TrimSpace(s), choice.Name) {
			return choice.Value, true
		}
	}

	return 0, false
}

func hasChoice(choices []Choice, n int64) bool {
	for _, choice := range choices {
		if choice.Value == n {
			return true
		}
	}

	return false
}

// duration reads integer milliseconds, or a Go duration from text.
func duration(raw any) (time.Duration, bool) {
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		if _, err := strconv.ParseInt(s, 10, 64); err != nil {
			d, err := time.ParseDuration(s)
			return d, err == nil
		}
	}

	ms, ok := integer(raw)
	if !ok || ms > math.MaxInt64/int64(time.Millisecond) || ms < math.MinInt64/int64(time.Millisecond) {
		return 0, false
	}

	return time.Duration(ms) * time.Millisecond, true
}

// list reads a list of scalars, or a comma-separated string.
func list(raw any) ([]string, bool) {
	switch r := raw.(type) {
	case []any:
		items := make([]string, 0, len(r))
		for _, item := range r {
			s, ok := scalarText(item)
			if !ok {
				return nil, false
			}

			items = append(items, s)
		}

		return items, true
	case string:
		items := []string{}
		for item := range strings.SplitSeq(r, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}

		return items, true
	default:
		s, ok := scalarText(raw)
		if !ok {
			return nil, false
		}

		return []string{s}, true
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

	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Sprint(raw)
	}

	return string(b)
}

// describe names the type of a decoded value, for messages.
func describe(raw any) string {
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
	default:
		return fmt.Sprintf("a %T", raw)
	}
}
