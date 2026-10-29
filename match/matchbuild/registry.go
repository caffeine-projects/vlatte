package matchbuild

import (
	"errors"
	"fmt"
	"strings"

	"github.com/caffeine-projects/vlatte"
)

var (
	_ vlatte.Matcher         = (*customMatcher)(nil)
	_ vlatte.SelfDescriptive = (*customMatcher)(nil)
)

var (
	// ErrNameEmpty is the error of a custom matcher with no name.
	ErrNameEmpty = errors.New("matchbuild: matcher name is empty")
	// ErrNameTaken is the error of a custom matcher whose name a built-in matcher, or another custom one, has.
	ErrNameTaken = errors.New("matchbuild: matcher name is taken")
)

// BuildFunc builds a custom matcher from the arguments of its spec, as a mock file gives them: strings, int64 and
// float64 numbers, bools, nil, lists ([]any) and objects (map[string]any). For ["body-too-long", 2048], args is
// [2048]. r is the registry building the spec: r.BuildMatcher builds an argument that is itself a matcher spec,
// custom names included.
//
// An error rejects the spec, so a mock file that holds it doesn't load. The matcher may return errors from Match,
// but an error stops the search for a mock, so it should only report a failure of its own.
type BuildFunc func(r *Registry, args []any) (vlatte.Matcher, error)

// Registry builds matcher specs: the built-in matchers, and the custom ones it was given. It can't change once
// built, so servers can share one.
type Registry struct {
	// builtins is the same map in every registry. It is a field, not read from a package variable, because building
	// a spec reaches back to the registry for nested specs: a variable would refer to itself as it is initialized.
	builtins map[string]builtin
	custom   map[string]namedBuild // by lowercase name
}

// namedBuild is a custom matcher's builder and its name, as given.
type namedBuild struct {
	name  string
	build BuildFunc
}

// Option configures a [Registry].
type Option interface {
	apply(o *options)
}

type options struct {
	matchers []namedBuild
}

type matcherOption namedBuild

func (m matcherOption) apply(o *options) { o.matchers = append(o.matchers, namedBuild(m)) }

// WithMatcher adds a custom matcher: specs that name it, such as [name, args...], are built with build. Names are
// read regardless of case, and can be neither a built-in matcher's name nor another custom one's.
func WithMatcher(name string, build BuildFunc) Option {
	return matcherOption{name: name, build: build}
}

// New creates a Registry of the built-in matchers and the custom ones that options give. A name that is empty,
// taken by a built-in matcher or given twice fails with an error wrapping [ErrNameEmpty] or [ErrNameTaken], one for
// each such name.
func New(opts ...Option) (*Registry, error) {
	var o options
	for _, opt := range opts {
		opt.apply(&o)
	}

	r := &Registry{builtins: _builtins, custom: make(map[string]namedBuild, len(o.matchers))}

	var errs error

	for _, m := range o.matchers {
		key := strings.ToLower(m.name)
		_, isBuiltin := r.builtins[key]
		_, isCustom := r.custom[key]

		switch {
		case m.name == "":
			errs = errors.Join(errs, ErrNameEmpty)
		case isBuiltin || isCustom:
			errs = errors.Join(errs, fmt.Errorf("%w: %q", ErrNameTaken, m.name))
		case m.build == nil:
			errs = errors.Join(errs, fmt.Errorf("matchbuild: matcher %q has no build function", m.name))
		default:
			r.custom[key] = m
		}
	}

	if errs != nil {
		return nil, errs
	}

	return r, nil
}

// MustNew is like [New], but panics on an error.
func MustNew(opts ...Option) *Registry {
	r, err := New(opts...)
	if err != nil {
		panic(err)
	}

	return r
}

// _builtins is built once: the built-in matchers by name.
var _builtins = newBuiltins()

// custom builds a custom matcher. One that doesn't describe itself describes itself as its spec, so a mock that
// holds it loads back from its description.
func (s spec) custom(b namedBuild) (vlatte.Matcher, error) {
	m, err := b.build(s.registry, s.args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	if m == nil {
		return nil, fmt.Errorf("%s built no matcher", s.name)
	}

	if _, ok := m.(vlatte.SelfDescriptive); ok {
		return m, nil
	}

	return &customMatcher{matcher: m, desc: vlatte.DescribeMultiArgs(b.name, s.args...)}, nil
}

// customMatcher is a custom matcher that describes itself as the spec it was built from.
type customMatcher struct {
	matcher vlatte.Matcher
	desc    vlatte.Descriptor
}

// Name implements vlatte.Matcher.
func (m *customMatcher) Name() string { return m.matcher.Name() }

// Match implements vlatte.Matcher.
func (m *customMatcher) Match(arg vlatte.MatcherArg) (bool, error) { return m.matcher.Match(arg) }

// Describe implements vlatte.SelfDescriptive.
func (m *customMatcher) Describe() vlatte.Descriptor { return m.desc }
