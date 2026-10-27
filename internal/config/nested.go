package config

import (
	"fmt"
	"maps"
	"slices"
)

// Decode applies the object an Any value holds to dst, the way Load applies a document.
// It is how a Set decodes a nested object with a schema of its own.
// Problems are keyed under v.Key, such as "response.status", and come from v.Origin.
// The error is an *Error listing every problem; returned by a Set, its problems join the caller's.
func (s *Schema[T]) Decode(dst *T, v Value) error {
	obj, ok := v.Any.(map[string]any)
	if !ok {
		return &Error{Problems: []Problem{mismatch(v.Key, v.Origin, v.Any, "an object")}}
	}

	w := walker{catalog: &s.catalog, origin: v.Origin, outer: v.Key}
	w.walk(obj, "")

	problems := append(w.problems, s.apply(dst, w.entries, make(map[string]Value), v.Key)...)
	if len(problems) > 0 {
		sortProblems(problems)
		return &Error{Problems: problems}
	}

	return nil
}

// Items calls fn with every element of the list an Any value holds, in order, as a Value keyed "<key>[i]".
// An error from fn becomes a problem of that element; an *Error keeps its own problems.
// A value that isn't a list is a problem of v. The error is an *Error listing every problem.
func Items(v Value, fn func(item Value) error) error {
	list, ok := v.Any.([]any)
	if !ok {
		return &Error{Problems: []Problem{mismatch(v.Key, v.Origin, v.Any, "a list")}}
	}

	var problems []Problem
	for i, e := range list {
		item := Value{Key: fmt.Sprintf("%s[%d]", v.Key, i), Origin: v.Origin, Raw: rawText(e), Any: e}
		if err := fn(item); err != nil {
			problems = append(problems, problemsOf(item, err)...)
		}
	}

	return asError(problems)
}

// Members calls fn with every member of the object an Any value holds, sorted by name,
// as a Value keyed "<key>.<name>". The names are kept as written, such as header names.
// An error from fn becomes a problem of that member; an *Error keeps its own problems.
// A value that isn't an object is a problem of v. The error is an *Error listing every problem.
func Members(v Value, fn func(name string, member Value) error) error {
	obj, ok := v.Any.(map[string]any)
	if !ok {
		return &Error{Problems: []Problem{mismatch(v.Key, v.Origin, v.Any, "an object")}}
	}

	var problems []Problem
	for _, name := range slices.Sorted(maps.Keys(obj)) {
		member := Value{Key: v.Key + "." + name, Origin: v.Origin, Raw: rawText(obj[name]), Any: obj[name]}
		if err := fn(name, member); err != nil {
			problems = append(problems, problemsOf(member, err)...)
		}
	}

	return asError(problems)
}

// MemberStrings calls fn with each string of each member of the object an Any value holds, sorted by name and then
// in order, as Members does: a member is a string, or a list of strings, such as the values of a header. A member of
// another type, or an item of a list that isn't a string, is a problem of its own. The error is an *Error listing
// every problem.
func MemberStrings(v Value, fn func(name, s string)) error {
	return Members(v, func(name string, member Value) error {
		switch x := member.Any.(type) {
		case string:
			fn(name, x)
		case []any:
			return Items(member, func(item Value) error {
				s, ok := item.Any.(string)
				if !ok {
					return Mismatch(item, "a string", String)
				}

				fn(name, s)

				return nil
			})
		default:
			return Mismatch(member, "a string or a list of strings", String)
		}

		return nil
	})
}

// Invalid returns an *Error with one invalid_value problem of v: message says what is wrong, and hint how to fix it.
// A Set returns it to report a problem with a hint, which a plain error can't carry.
func Invalid(v Value, message, hint string) error {
	return &Error{Problems: []Problem{{
		Code:    CodeInvalidValue,
		Origin:  v.Origin,
		Key:     v.Key,
		Value:   display(v.Raw),
		Message: message,
		Hint:    hint,
	}}}
}

// Mismatch returns an *Error with the invalid_value problem of v, which holds a value of a type the setting doesn't
// take, worded as every such problem is: "<key> must be <want>, not <a number>". takes lists the kinds the setting
// takes, which pick the hint: a number or a bool where a String is taken is to be quoted, an integer in quotes
// where an Int is taken is not, and text where a List is taken is to be written as a list.
func Mismatch(v Value, want string, takes ...Kind) error {
	return &Error{Problems: []Problem{mismatch(v.Key, v.Origin, v.Any, want, takes...)}}
}

// ProblemsOf returns the problems of err, which Decode or a Set returned for v: none for nil, the problems of an
// *Error, and one problem of v for any other error. A caller adding problems of its own merges them, and drops no
// error.
func ProblemsOf(v Value, err error) []Problem {
	if err == nil {
		return nil
	}

	return problemsOf(v, err)
}

// asError returns problems as an *Error, or nil when there are none.
func asError(problems []Problem) error {
	if len(problems) == 0 {
		return nil
	}

	return &Error{Problems: problems}
}
