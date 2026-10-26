// Package config loads settings from files, environment variables and command-line arguments
// into a configuration struct, following a schema that names, types and documents every setting.
//
// Problems are collected, not returned one at a time: Load reports every invalid setting it finds,
// each with where it came from and how to fix it.
package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Kind is the type of a setting's value.
type Kind uint8

// Kinds of setting values. A value in a document must have the type of its kind: nothing is converted,
// except that bools read everywhere the way strconv.ParseBool reads text. Env and args give text, which is parsed.
const (
	// String takes a string.
	String Kind = iota
	// Bool takes what strconv.ParseBool reads: a bool, text such as true, 1 or f, or the number 1 or 0.
	Bool
	// Int takes a whole number that fits an int64. Text gives it in the JSON number grammar, so 5 and 5.0,
	// but not +5 or 05. With Choices, it takes only the name of a choice, in any case.
	Int
	// Duration takes whole milliseconds from 0, or an unsigned Go duration, such as 1.5s.
	// Text that is a number is milliseconds.
	Duration
	// List takes a list of strings. Text gives comma-separated items, and a repeated flag adds to them.
	List
	// Any takes any value as decoded, unchecked: a Set decodes it, for example a nested object with Decode.
	// Env and args give it their text.
	Any
)

func (k Kind) String() string {
	switch k {
	case String:
		return "string"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Duration:
		return "duration"
	case List:
		return "list"
	case Any:
		return "any"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// Choice names an allowed value of an Int setting.
type Choice struct {
	Name  string
	Value int64
}

// Field declares a setting of a T.
type Field[T any] struct {
	// Key is the dotted, snake_case path of the setting, such as "log.level".
	// It is the nested key in files. Env and flag names derive from it.
	Key string
	// Short is an optional one-letter flag alias.
	Short string
	// Usage describes the setting in help text.
	Usage string
	// Default is the default value as help text shows it. It is not applied.
	Default string
	// Kind is the type of the value.
	Kind Kind
	// Choices lists the allowed values of an Int setting. Inputs give their names, in any case.
	Choices []Choice
	// Set validates the value and applies it to dst. Its error becomes a problem of the value.
	// A nil Set leaves the value to the caller, which reads it from Load's result.
	Set func(dst *T, v Value) error
	// Hidden keeps the setting out of Flags and Usage: it is read, and checked, as any other.
	Hidden bool
}

// Value is a setting's value, converted to its Kind.
type Value struct {
	Key    string // the setting's key
	Origin string // where the value came from, such as "env VLATTE_ADDR" or "flag --addr"
	Raw    string // the value as written

	Str  string
	Bool bool
	Int  int64
	Dur  time.Duration
	List []string
	// Any is the value of an Any setting, as decoded: nil, bool, string, int64, float64, []any or map[string]any.
	Any any
}

// Flag describes the command-line flag of a setting.
type Flag struct {
	Name      string   // long name without dashes, such as "log-level"
	Shorthand string   // one-letter alias, or ""
	Usage     string   // the setting's usage
	Bool      bool     // the flag takes no value
	Values    []string // names of the allowed values
}

type field struct {
	key     string
	short   string
	usage   string
	def     string
	kind    Kind
	choices []Choice
	flag    string // derived flag name, such as "log-level"
	env     string // derived environment variable name without prefix, such as "LOG_LEVEL"
	section bool   // a Bool key that other keys nest under, such as "cors"
	hidden  bool   // left out of Flags and Usage
}

// catalog is the view of a schema that sources use. It doesn't depend on the target type.
type catalog struct {
	fields   []*field
	byKey    map[string]*field
	byFlag   map[string]*field   // long names and shorthands
	groups   map[string]struct{} // prefixes with no key of their own, such as "log"
	loadOnly bool                // sources read only its keys, and let every other one through: see Schema.LoadOnly

	// skip and only name the top-level settings files and environment variables leave to other schemas, that read
	// the same sources: those of skip, or all but those of only.
	skip []string
	only []string
}

// Schema holds the settings of a T, in the order they were declared.
type Schema[T any] struct {
	catalog

	sets map[string]func(*T, Value) error
}

var (
	_keySegment = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	_flagName   = strings.NewReplacer(".", "-", "_", "-")
)

// NewSchema builds a schema from fields.
// It panics when a key, flag or environment variable name is invalid or used twice, since fields are declared in code.
func NewSchema[T any](fields ...Field[T]) *Schema[T] {
	s := &Schema[T]{sets: make(map[string]func(*T, Value) error, len(fields))}
	s.byKey = make(map[string]*field, len(fields))
	s.byFlag = make(map[string]*field, len(fields))
	s.groups = make(map[string]struct{})

	for _, decl := range fields {
		for segment := range strings.SplitSeq(decl.Key, ".") {
			if !_keySegment.MatchString(segment) {
				panic(fmt.Sprintf("config: invalid key %q: use dotted snake_case, such as log.level", decl.Key))
			}
		}

		if _, ok := s.byKey[decl.Key]; ok {
			panic(fmt.Sprintf("config: key %q is declared twice", decl.Key))
		}

		if len(decl.Choices) > 0 && decl.Kind != Int {
			panic(fmt.Sprintf("config: key %q has choices, but only Int settings can", decl.Key))
		}

		if len(decl.Short) > 1 {
			panic(fmt.Sprintf("config: short flag %q of key %q must be one letter", decl.Short, decl.Key))
		}

		f := &field{
			key:     decl.Key,
			short:   decl.Short,
			usage:   decl.Usage,
			def:     decl.Default,
			kind:    decl.Kind,
			choices: decl.Choices,
			flag:    _flagName.Replace(decl.Key),
			env:     strings.ToUpper(strings.ReplaceAll(decl.Key, ".", "_")),
			hidden:  decl.Hidden,
		}

		// Two keys can't share an environment variable without sharing a flag: both names turn "." and "_"
		// into one character. So this check covers environment variables too.
		for _, name := range []string{f.flag, f.short} {
			if name == "" {
				continue
			}

			if name == "h" || name == "help" {
				panic(fmt.Sprintf("config: key %q can't use flag %q: it asks for help", decl.Key, name))
			}

			if other, ok := s.byFlag[name]; ok {
				panic(fmt.Sprintf("config: keys %q and %q both use flag %q", other.key, decl.Key, name))
			}

			s.byFlag[name] = f
		}

		s.fields = append(s.fields, f)
		s.byKey[decl.Key] = f

		if decl.Set != nil {
			s.sets[decl.Key] = decl.Set
		}
	}

	// Every prefix of a key is either a section (a Bool key) or a group (no key of its own).
	for _, f := range s.fields {
		parts := strings.Split(f.key, ".")
		for i := 1; i < len(parts); i++ {
			prefix := strings.Join(parts[:i], ".")
			parent, ok := s.byKey[prefix]

			switch {
			case !ok:
				s.groups[prefix] = struct{}{}
			case parent.kind == Bool:
				parent.section = true
			default:
				panic(fmt.Sprintf("config: key %q nests under %q, which is not a bool", f.key, prefix))
			}
		}
	}

	return s
}

// Skip leaves the top-level settings names, such as "grpc", to another schema that reads the same files and
// environment variables: the schema ignores them there, where it would report them as unknown. Flags are the
// caller's to split: an Args source reports every flag it doesn't know.
func (s *Schema[T]) Skip(names ...string) *Schema[T] {
	s.skip = append(s.skip, names...)
	return s
}

// Only reads only the top-level settings names, such as "grpc", from files and environment variables: the schema
// leaves the others to the schemas that read the same sources, as Skip does.
func (s *Schema[T]) Only(names ...string) *Schema[T] {
	s.only = append(s.only, names...)
	return s
}

// Flags lists the command-line flags of the settings, in declaration order.
func (c *catalog) Flags() []Flag {
	flags := make([]Flag, 0, len(c.fields))
	for _, f := range c.fields {
		if f.hidden {
			continue
		}

		flag := Flag{Name: f.flag, Shorthand: f.short, Usage: f.usage, Bool: f.kind == Bool}
		for _, choice := range f.choices {
			flag.Values = append(flag.Values, choice.Name)
		}

		flags = append(flags, flag)
	}

	return flags
}

// Usage describes every setting: its flag, environment variable, file key, allowed values and default.
func (c *catalog) Usage(envPrefix string) string {
	var b strings.Builder

	for _, f := range c.fields {
		if f.hidden {
			continue
		}

		b.WriteString("  ")
		if f.short != "" {
			fmt.Fprintf(&b, "-%s, ", f.short)
		}

		fmt.Fprintf(&b, "--%s", f.flag)

		switch {
		case f.kind == Bool:
		case len(f.choices) > 0:
			b.WriteString(" name")
		default:
			fmt.Fprintf(&b, " %s", f.kind)
		}

		fmt.Fprintf(&b, "\n        %s", f.usage)
		if len(f.choices) > 0 {
			fmt.Fprintf(&b, " One of: %s.", strings.Join(choiceNames(f.choices), ", "))
		}

		if f.def != "" {
			fmt.Fprintf(&b, " Default: %s.", f.def)
		}

		fmt.Fprintf(&b, "\n        env %s_%s, file key %s\n", envPrefix, f.env, f.key)
	}

	return b.String()
}

// Keys lists every key and group, sorted, such as "log" and "log.level".
func (c *catalog) Keys() []string {
	keys := make([]string, 0, len(c.fields)+len(c.groups))
	for _, f := range c.fields {
		keys = append(keys, f.key)
	}

	for group := range c.groups {
		keys = append(keys, group)
	}

	slices.Sort(keys)

	return keys
}

// KindOf returns the kind of the setting key, or false when there is no such setting.
func (c *catalog) KindOf(key string) (Kind, bool) {
	f, ok := c.byKey[key]
	if !ok {
		return 0, false
	}

	return f.kind, true
}

// owns reports whether the top-level setting name is the schema's to read from files.
func (c *catalog) owns(name string) bool {
	if len(c.only) > 0 && !slices.Contains(c.only, name) {
		return false
	}

	return !slices.Contains(c.skip, name)
}

// ownsEnv reports whether the environment variable name, without its prefix and underscore, such as GRPC_ADDR, is
// the schema's to read.
func (c *catalog) ownsEnv(name string) bool {
	under := func(names []string) bool {
		return slices.ContainsFunc(names, func(n string) bool {
			env := strings.ToUpper(n)
			return name == env || strings.HasPrefix(name, env+"_")
		})
	}

	if len(c.only) > 0 && !under(c.only) {
		return false
	}

	return !under(c.skip)
}

// children lists the names right under prefix, of keys and groups, in declaration order.
// An empty prefix lists the top-level names.
func (c *catalog) children(prefix string) []string {
	var names []string
	seen := make(map[string]struct{})

	for _, f := range c.fields {
		rest := f.key
		if prefix != "" {
			var ok bool
			if rest, ok = strings.CutPrefix(f.key, prefix+"."); !ok {
				continue
			}
		}

		name, _, _ := strings.Cut(rest, ".")
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}

	return names
}
