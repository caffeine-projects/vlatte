// Package filterbuilder builds matchers from the matcher specs of mock files: a list whose first item names the
// matcher and whose other items are its arguments, such as ["contains", "x"] or ["jsonpath", "$.a", ["gt", 1]].
// Arguments are checked when a spec is built, so a mock file with a bad spec doesn't load, and nothing a spec
// builds fails on a request.
package filterbuilder

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
)

const (
	_mAll                     = "all"
	_mAny                     = "any"
	_mAnything                = "anything"
	_mBoth                    = "both"
	_mContains                = "contains"
	_mEach                    = "each"
	_mEither                  = "either"
	_mEmpty                   = "empty"
	_mEqualTo                 = "equal"
	_mEqualToAlias            = "eq"
	_mEqualToIgnoreCase       = "equalignorecase"
	_mEqualToIgnoreCaseAlias  = "eqi"
	_mEqualJSON               = "equaljson"
	_mEqualJSONAlias          = "eqj"
	_mEqualToStrict           = "equalstrict"
	_mEqualToStrictAlias      = "eqs"
	_mFalsy                   = "falsy"
	_mGlob                    = "glob"
	_mGreater                 = "greater"
	_mGreaterAlias            = "gt"
	_mGreaterThanOrEqual      = "greatereq"
	_mGreaterThanOrEqualAlias = "gte"
	_mHasKey                  = "haskey"
	_mHasPrefix               = "hasprefix"
	_mHasSuffix               = "hassuffix"
	_mIsIn                    = "isin"
	_mItem                    = "item"
	_mItemsMatch              = "itemsmatch"
	_mJSONPath                = "jsonpath"
	_mField                   = "field"
	_mLength                  = "length"
	_mLen                     = "len"
	_mLowerCase               = "lowercase"
	_mLessThan                = "less"
	_mLessThanAlias           = "lt"
	_mLessThanOrEqual         = "lesseq"
	_mLessThanOrEqualAlias    = "lte"
	_mNil                     = "nil"
	_mRegex                   = "regex"
	_mRegexAlias              = "regexp" // the name match.RegExpr describes itself with
	_mRequestURI              = "requesturi"
	_mSome                    = "some"
	_mNot                     = "not"
	_mPresent                 = "present"
	_mSplit                   = "split"
	_mTrim                    = "trim"
	_mTruthy                  = "truthy"
	_mUpperCase               = "uppercase"
	_mURLPath                 = "urlpath"
	_mXOR                     = "xor"
)

var errEmptySpec = errors.New(`a matcher spec names its matcher first, as in ["equal", 1]`)

// MatcherOf returns the matcher of a value of a mock file, v: text matches equal text, and a list is a matcher spec.
func MatcherOf(v config.Value) (vlatte.Matcher, error) {
	switch x := v.Any.(type) {
	case string:
		return TryBuildMatcher(x)
	case []any:
		if len(x) == 0 {
			return nil, config.Invalid(v, v.Key+" is an empty list", "name a matcher, such as [equal, x]")
		}

		return TryBuildMatcher(x)
	default:
		return nil, config.Mismatch(v, "a string or a matcher list, such as [equal, x]", config.String)
	}
}

// TryBuildMatcher builds the matcher of a value of a mock file: a string matches text equal to it,
// and a list is a matcher spec.
func TryBuildMatcher(spec any) (vlatte.Matcher, error) {
	if s, ok := spec.(string); ok {
		return match.Equal(s), nil
	}

	return BuildMatcher(spec)
}

// BuildMatcher builds the matcher a spec names: a matcher's name alone, such as "present",
// or a list such as ["contains", "x"].
func BuildMatcher(spec any) (vlatte.Matcher, error) {
	switch s := spec.(type) {
	case string:
		return build(s, nil)
	case []any:
		if len(s) == 0 {
			return nil, errEmptySpec
		}

		name, ok := s[0].(string)
		if !ok {
			return nil, fmt.Errorf(`a matcher spec names its matcher first, as in ["equal", 1], not %s`, config.Describe(s[0]))
		}

		return build(name, s[1:])
	default:
		return nil, fmt.Errorf(`a matcher is a name or a definition such as ["equal", 1], not %s`, config.Describe(spec))
	}
}

func build(name string, args []any) (vlatte.Matcher, error) {
	s := spec{name: name, args: args}
	if err := s.finite(args); err != nil {
		return nil, err
	}

	switch strings.ToLower(name) {
	case _mAnything:
		return s.none(match.Anything())
	case _mEmpty:
		return s.none(match.Empty())
	case _mFalsy:
		return s.none(match.Falsy())
	case _mNil:
		return s.none(match.Nil())
	case _mPresent:
		return s.none(match.Present())
	case _mTruthy:
		return s.none(match.Truthy())

	case _mEqualTo, _mEqualToAlias:
		return s.one(match.Equal)
	case _mEqualToStrict, _mEqualToStrictAlias:
		return s.one(match.StrictEqual)
	case _mContains:
		return s.one(match.Contain)
	case _mEqualJSON, _mEqualJSONAlias:
		return s.json()
	case _mIsIn:
		return s.items(match.IsIn[any])
	case _mItemsMatch:
		return s.items(match.ItemsMatch[any])

	case _mEqualToIgnoreCase, _mEqualToIgnoreCaseAlias:
		return s.text("a string", match.EqualIgnoreCase, nil)
	case _mGlob:
		return s.text("a glob pattern", match.GlobMatch, nil)
	case _mHasPrefix:
		return s.text("a string", match.HasPrefix, nil)
	case _mHasSuffix:
		return s.text("a string", match.HasSuffix, nil)
	case _mRegex, _mRegexAlias:
		return s.text("a regular expression", match.RegExpr, func(pattern string) error {
			_, err := regexp.Compile(pattern)
			return err
		})
	case _mRequestURI:
		return s.text("a URL", match.RequestURI, checkRequestURI)
	case _mHasKey:
		return s.text("a JSON path", match.HasKey, func(path string) error {
			_, err := jp.ParseString(path)
			return err
		})

	case _mGreater, _mGreaterAlias:
		return s.order(match.GreaterThan[int64], match.GreaterThan[float64])
	case _mGreaterThanOrEqual, _mGreaterThanOrEqualAlias:
		return s.order(match.GreaterThanOrEqual[int64], match.GreaterThanOrEqual[float64])
	case _mLessThan, _mLessThanAlias:
		return s.order(match.LessThan[int64], match.LessThan[float64])
	case _mLessThanOrEqual, _mLessThanOrEqualAlias:
		return s.order(match.LessThanOrEqual[int64], match.LessThanOrEqual[float64])
	case _mLen, _mLength:
		if err := s.arity(1); err != nil {
			return nil, err
		}

		n, err := s.count(0, "a length")
		if err != nil {
			return nil, err
		}

		return match.Len(n), nil

	case _mEach:
		return s.inner(match.Each)
	case _mSome:
		return s.inner(match.Some)
	case _mNot:
		return s.inner(match.Not)
	case _mTrim:
		return s.inner(match.Trim)
	case _mLowerCase:
		return s.inner(match.ToLower)
	case _mUpperCase:
		return s.inner(match.ToUpper)
	case _mURLPath:
		if err := s.arity(1); err != nil {
			return nil, err
		}

		if pattern, ok := s.args[0].(string); ok {
			return match.URLPath(pattern), nil
		}

		return s.inner(match.URLPathMatch)

	case _mItem:
		return s.item()
	case _mJSONPath, _mField:
		return s.jsonPath()
	case _mSplit:
		return s.split()

	case _mBoth:
		return s.pair(match.Both)
	case _mEither:
		return s.pair(match.Either)
	case _mXOR:
		return s.pair(match.XOR)
	case _mAll:
		return s.many(match.All)
	case _mAny:
		return s.many(match.Any)

	default:
		return nil, fmt.Errorf("unknown matcher %q", name)
	}
}

// spec is a matcher's name and arguments, as written, for checks whose errors name the matcher.
type spec struct {
	name string
	args []any
}

// arity checks that the spec has n arguments. A matcher that takes one value gets a hint to write many as a list.
func (s spec) arity(n int) error {
	if len(s.args) == n {
		return nil
	}

	err := fmt.Errorf("%s takes %s, not %d", s.name, plural(n, "argument"), len(s.args))
	if n == 1 && len(s.args) > 1 {
		return fmt.Errorf("%w; write a list as one argument: [%s, [...]]", err, s.name)
	}

	return err
}

// takes is the error of an argument of the wrong type.
func (s spec) takes(want string, arg any, hint string) error {
	err := fmt.Errorf("%s takes %s, not %s", s.name, want, config.Describe(arg))
	if hint != "" {
		return fmt.Errorf("%w; %s", err, hint)
	}

	return err
}

// finite rejects NaN and infinities anywhere in args: no request value equals them, and JSON can't write them.
func (s spec) finite(args []any) error {
	for _, arg := range args {
		switch x := arg.(type) {
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return fmt.Errorf("%s takes finite numbers, not %v", s.name, x)
			}
		case []any:
			if err := s.finite(x); err != nil {
				return err
			}
		case map[string]any:
			for _, member := range x {
				if err := s.finite([]any{member}); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (s spec) none(m vlatte.Matcher) (vlatte.Matcher, error) {
	if err := s.arity(0); err != nil {
		return nil, err
	}

	return m, nil
}

func (s spec) one(build func(any) vlatte.Matcher) (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	return build(s.args[0]), nil
}

// json builds EqualJSON of JSON text, or of a value written as JSON.
func (s spec) json() (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	var err error
	if text, ok := s.args[0].(string); ok {
		_, err = value.DecodeJSON([]byte(text))
	} else {
		_, err = value.ViaJSON(s.args[0])
	}

	if err != nil {
		return nil, fmt.Errorf("%s takes JSON: %w", s.name, err)
	}

	return match.EqualJSON(s.args[0]), nil
}

func (s spec) items(build func([]any) vlatte.Matcher) (vlatte.Matcher, error) {
	if len(s.args) > 1 {
		return nil, fmt.Errorf("%s takes 1 argument, a list of the items, not %d; write [%s, [...]]", s.name, len(s.args), s.name)
	}

	if err := s.arity(1); err != nil {
		return nil, err
	}

	items, ok := s.args[0].([]any)
	if !ok {
		return nil, s.takes("a list", s.args[0], "")
	}

	return build(items), nil
}

func (s spec) text(want string, build func(string) vlatte.Matcher, check func(string) error) (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	text, ok := s.args[0].(string)
	if !ok {
		return nil, s.takes(want, s.args[0], quoteHint(s.args[0]))
	}

	if check != nil {
		if err := check(text); err != nil {
			return nil, fmt.Errorf("%s takes %s, not %q: %w", s.name, want, text, err)
		}
	}

	return build(text), nil
}

// order builds an order matcher of a number, as documents give it, keeping its type: an integer bound compares
// exactly.
func (s spec) order(ints func(int64) vlatte.Matcher, floats func(float64) vlatte.Matcher) (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	switch x := s.args[0].(type) {
	case int64:
		return ints(x), nil
	case float64:
		return floats(x), nil
	default:
		return nil, s.takes("a number", s.args[0], unquoteHint(s.args[0]))
	}
}

// count reads the argument at i as a whole number from 0, such as a length or an index.
func (s spec) count(i int, what string) (int, error) {
	want := what + ", a whole number from 0"

	n, isNumber := value.Number(s.args[i])
	if !isNumber {
		return 0, s.takes(want, s.args[i], unquoteHint(s.args[i]))
	}

	if x, ok := n.(int64); ok && x >= 0 && x <= math.MaxInt {
		return int(x), nil
	}

	return 0, fmt.Errorf("%s takes %s, not %v", s.name, want, s.args[i])
}

// matcher builds the argument at i as a matcher spec.
func (s spec) matcher(i int) (vlatte.Matcher, error) {
	m, err := BuildMatcher(s.args[i])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.name, err)
	}

	return m, nil
}

func (s spec) inner(build func(vlatte.Matcher) vlatte.Matcher) (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	m, err := s.matcher(0)
	if err != nil {
		return nil, err
	}

	return build(m), nil
}

// item builds ["item", index, matcher].
func (s spec) item() (vlatte.Matcher, error) {
	if err := s.arity(2); err != nil {
		return nil, err
	}

	index, err := s.count(0, "an index")
	if err != nil {
		return nil, err
	}

	m, err := s.matcher(1)
	if err != nil {
		return nil, err
	}

	return match.Item(index, m), nil
}

// jsonPath builds ["jsonpath", path, matcher].
func (s spec) jsonPath() (vlatte.Matcher, error) {
	if err := s.arity(2); err != nil {
		return nil, err
	}

	path, ok := s.args[0].(string)
	if !ok {
		return nil, s.takes("a JSON path first", s.args[0], "")
	}

	if _, err := jp.ParseString(path); err != nil {
		return nil, fmt.Errorf("%s takes a JSON path first, not %q: %w", s.name, path, err)
	}

	m, err := s.matcher(1)
	if err != nil {
		return nil, err
	}

	return match.Field(path, m), nil
}

// split builds ["split", separator, matcher].
func (s spec) split() (vlatte.Matcher, error) {
	if err := s.arity(2); err != nil {
		return nil, err
	}

	separator, ok := s.args[0].(string)
	if !ok {
		return nil, s.takes("a separator first", s.args[0], quoteHint(s.args[0]))
	}

	m, err := s.matcher(1)
	if err != nil {
		return nil, err
	}

	return match.Split(separator, m), nil
}

// matchers reads the arguments as matchers: each argument is one, or the only argument is a list of them.
func (s spec) matchers() ([]vlatte.Matcher, error) {
	specs := s.args
	if len(s.args) == 1 {
		if list, ok := s.args[0].([]any); ok {
			specs = list
		}
	}

	matchers := make([]vlatte.Matcher, 0, len(specs))
	for i, entry := range specs {
		switch entry.(type) {
		case string, []any:
		default:
			return nil, fmt.Errorf(`%s: matcher %d: a matcher is a name or a definition such as ["equal", 1], not %s`,
				s.name, i, config.Describe(entry))
		}

		m, err := BuildMatcher(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: matcher %d: %w", s.name, i, err)
		}

		matchers = append(matchers, m)
	}

	return matchers, nil
}

func (s spec) pair(build func(first, second vlatte.Matcher) vlatte.Matcher) (vlatte.Matcher, error) {
	matchers, err := s.matchers()
	if err != nil {
		return nil, err
	}

	if len(matchers) != 2 {
		return nil, fmt.Errorf("%s takes 2 matchers, not %d", s.name, len(matchers))
	}

	return build(matchers[0], matchers[1]), nil
}

func (s spec) many(build func(...vlatte.Matcher) vlatte.Matcher) (vlatte.Matcher, error) {
	matchers, err := s.matchers()
	if err != nil {
		return nil, err
	}

	if len(matchers) == 0 {
		return nil, fmt.Errorf("%s takes 1 matcher or more, not 0", s.name)
	}

	return build(matchers...), nil
}

// quoteHint suggests quotes for a number or a bool where text is taken.
func quoteHint(arg any) string {
	switch arg.(type) {
	case bool, int64, float64:
		return fmt.Sprintf("write it in quotes: \"%v\"", arg)
	default:
		return ""
	}
}

// unquoteHint suggests no quotes for text that reads as a number where one is taken.
func unquoteHint(arg any) string {
	if text, ok := arg.(string); ok {
		if _, isNumber := value.ParseNumber(text); isNumber {
			return "write it without quotes: " + text
		}
	}

	return ""
}

func plural(n int, noun string) string {
	switch n {
	case 0:
		return "no " + noun
	case 1:
		return "1 " + noun
	default:
		return fmt.Sprintf("%d %ss", n, noun)
	}
}

// checkRequestURI checks a URL as match.RequestURI reads it. One that starts with "/" is a request target, whose path
// a "//" starts, as a server reads it, not a host. Another must be absolute: a request's path starts at "/", so a
// relative one that doesn't would never match.
func checkRequestURI(uri string) error {
	if !strings.HasPrefix(uri, "/") {
		u, err := url.Parse(uri)
		if err == nil && !u.IsAbs() {
			err = errors.New("its path must start with /")
		}

		return err
	}

	target, fragment, hasFragment := strings.Cut(uri, "#")
	if _, err := url.ParseRequestURI(target); err != nil {
		return err
	}

	if hasFragment {
		_, err := url.Parse("#" + fragment)
		return err
	}

	return nil
}
