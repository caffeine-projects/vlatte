// Package matchbuild builds matchers from the matcher specs of mock files: a list whose first item names the
// matcher and whose other items are its arguments, such as ["contains", "x"] or ["jsonpath", "$.a", ["gt", 1]].
// Arguments are checked when a spec is built, so a mock file with a bad spec doesn't load, and no built-in matcher a
// spec builds fails on a request: only a custom one can, as its [BuildFunc] makes it.
//
// A [Registry] builds specs: the built-in matchers, and the custom ones it was given with [WithMatcher].
// Each server reads its mock files with a registry of its own, or one several servers share.
package matchbuild

import (
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ohler55/ojg/jp"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/internal/timetext"
)

const (
	_mAfter                   = "after"
	_mAll                     = "all"
	_mAny                     = "any"
	_mAnything                = "anything"
	_mBefore                  = "before"
	_mBoth                    = "both"
	_mContains                = "contains"
	_mEach                    = "each"
	_mEither                  = "either"
	_mEmpty                   = "empty"
	_mEqualTo                 = "equal"
	_mEqualToAlias            = "eq"
	_mEqualToIgnoreCase       = "equalignorecase"
	_mEqualToIgnoreCaseAlias  = "eqi"
	_mEqualDateTime           = "equaldatetime"
	_mEqualDateTimeAlias      = "eqdt"
	_mEqualJSON               = "equaljson"
	_mEqualJSONAlias          = "eqj"
	_mEqualXML                = "equalxml"
	_mEqualXMLAlias           = "eqx"
	_mEqualToStrict           = "equalstrict"
	_mEqualToStrictAlias      = "eqs"
	_mEqualBytes              = "equalbytes"
	_mFalsy                   = "falsy"
	_mGlob                    = "glob"
	_mGreater                 = "greater"
	_mGreaterAlias            = "gt"
	_mGreaterThanOrEqual      = "greatereq"
	_mGreaterThanOrEqualAlias = "gte"
	_mHasExactly              = "hasexactly"
	_mHasKey                  = "haskey"
	_mHasPrefix               = "hasprefix"
	_mHasSuffix               = "hassuffix"
	_mIsIn                    = "isin"
	_mItem                    = "item"
	_mItemsMatch              = "itemsmatch"
	_mJSONPath                = "jsonpath"
	_mJSONSchema              = "jsonschema"
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
	_mXPath                   = "xpath"
)

// The options of matchers, as the object after their arguments names them.
const (
	_oIgnoreArrayOrder    = "ignore_array_order"
	_oIgnoreExtraElements = "ignore_extra_elements"
	_oDraft               = "draft"
	_oIgnoreChildOrder    = "ignore_child_order"
	_oNamespaces          = "namespaces"
	_oTruncateExpected    = "truncate_expected"
	_oTruncateActual      = "truncate_actual"
	_oTruncateLast        = "truncate_last"
	_oActualFormat        = "actual_format"
	_oLocalTime           = "local_time"
)

// _units are the units of the offset of a date and time, such as "now +3 days", by their names, plural.
var _units = byName(match.Seconds, match.Minutes, match.Hours, match.Days, match.Weeks, match.Months, match.Years)

// _truncations are the truncations of dates and times, by their names, such as "first day of month".
var _truncations = byName(match.FirstMinuteOfHour, match.FirstHourOfDay, match.FirstDayOfMonth, match.FirstDayOfNextMonth,
	match.LastDayOfMonth, match.FirstDayOfYear, match.FirstDayOfNextYear, match.LastDayOfYear)

// byName returns values by the names their String methods give.
func byName[T fmt.Stringer](values ...T) map[string]T {
	m := make(map[string]T, len(values))
	for _, v := range values {
		m[v.String()] = v
	}

	return m
}

// _namespaceModes are the ways EqualXML reads namespaces, by the names mock files give them.
var _namespaceModes = map[string]match.XMLNamespaceMode{
	match.XMLNamespacesByURI.String():   match.XMLNamespacesByURI,
	match.XMLNamespacesStrict.String():  match.XMLNamespacesStrict,
	match.XMLNamespacesIgnored.String(): match.XMLNamespacesIgnored,
}

// _drafts are the JSON Schema drafts by the names mock files give them.
var _drafts = map[string]match.JSONSchemaDraft{
	"4":       match.JSONSchemaDraft4,
	"6":       match.JSONSchemaDraft6,
	"7":       match.JSONSchemaDraft7,
	"2019-09": match.JSONSchemaDraft2019,
	"2020-12": match.JSONSchemaDraft2020,
}

var errEmptySpec = errors.New(`a matcher spec names its matcher first, as in ["equal", 1]`)

// TryBuildMatcher builds the matcher of a value of a mock file: a string matches text equal to it,
// and a list is a matcher spec.
func (r *Registry) TryBuildMatcher(spec any) (vlatte.Matcher, error) {
	if s, ok := spec.(string); ok {
		return match.Equal(s), nil
	}

	return r.BuildMatcher(spec)
}

// BuildMatcher builds the matcher a spec names: a matcher's name alone, such as "present",
// or a list such as ["contains", "x"]. The name is a built-in matcher's or one the registry was given.
func (r *Registry) BuildMatcher(spec any) (vlatte.Matcher, error) {
	switch s := spec.(type) {
	case string:
		return r.build(s, nil)
	case []any:
		if len(s) == 0 {
			return nil, errEmptySpec
		}

		name, ok := s[0].(string)
		if !ok {
			return nil, fmt.Errorf(`a matcher spec names its matcher first, as in ["equal", 1], not %s`, config.Describe(s[0]))
		}

		return r.build(name, s[1:])
	default:
		return nil, fmt.Errorf(`a matcher is a name or a definition such as ["equal", 1], not %s`, config.Describe(spec))
	}
}

func (r *Registry) build(name string, args []any) (vlatte.Matcher, error) {
	s := spec{name: name, args: args, registry: r}
	if err := s.finite(args); err != nil {
		return nil, err
	}

	key := strings.ToLower(name)
	if build, ok := r.builtins[key]; ok {
		return build(s)
	}

	if build, ok := r.custom[key]; ok {
		return s.custom(build)
	}

	return nil, fmt.Errorf("unknown matcher %q", name)
}

// builtin builds a built-in matcher from its spec.
type builtin func(s spec) (vlatte.Matcher, error)

// newBuiltins returns the built-in matchers by name, aliases included.
func newBuiltins() map[string]builtin {
	none := func(m vlatte.Matcher) builtin { return func(s spec) (vlatte.Matcher, error) { return s.none(m) } }
	one := func(build func(any) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.one(build) }
	}
	items := func(build func([]any) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.items(build) }
	}
	text := func(want string, build func(string) vlatte.Matcher, check func(string) error) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.text(want, build, check) }
	}
	order := func(ints func(int64) vlatte.Matcher, floats func(float64) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.order(ints, floats) }
	}
	inner := func(build func(vlatte.Matcher) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.inner(build) }
	}
	pair := func(build func(first, second vlatte.Matcher) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.pair(build) }
	}
	many := func(build func(...vlatte.Matcher) vlatte.Matcher) builtin {
		return func(s spec) (vlatte.Matcher, error) { return s.many(build) }
	}

	checkRegexp := func(pattern string) error {
		_, err := regexp.Compile(pattern)
		return err
	}
	checkJSONPath := func(path string) error {
		_, err := jp.ParseString(path)
		return err
	}
	checkBase64 := func(text string) error {
		_, err := base64.StdEncoding.DecodeString(text)
		return err
	}
	equalBytes := func(text string) vlatte.Matcher {
		b, _ := base64.StdEncoding.DecodeString(text) // checkBase64 checked it
		return match.EqualBytes(b)
	}

	builtins := map[string]builtin{
		_mAnything: none(match.Anything()),
		_mEmpty:    none(match.Empty()),
		_mFalsy:    none(match.Falsy()),
		_mNil:      none(match.Nil()),
		_mPresent:  none(match.Present()),
		_mTruthy:   none(match.Truthy()),

		_mEqualTo:       one(match.Equal),
		_mEqualToStrict: one(match.StrictEqual),
		_mContains:      one(match.Contain),
		_mEqualJSON:     spec.json,
		_mIsIn:          items(match.IsIn[any]),
		_mItemsMatch:    items(match.ItemsMatch[any]),
		_mHasExactly:    spec.hasExactly,
		_mJSONSchema:    spec.jsonSchema,
		_mEqualXML:      spec.xml,
		_mXPath:         spec.xpath,
		_mBefore:        dateTime(match.Before, match.BeforeNow),
		_mAfter:         dateTime(match.After, match.AfterNow),
		_mEqualDateTime: dateTime(match.EqualDateTime, match.EqualDateTimeNow),

		_mEqualToIgnoreCase: text("a string", match.EqualIgnoreCase, nil),
		_mGlob:              text("a glob pattern", match.GlobMatch, nil),
		_mHasPrefix:         text("a string", match.HasPrefix, nil),
		_mHasSuffix:         text("a string", match.HasSuffix, nil),
		_mRegex:             text("a regular expression", match.RegExpr, checkRegexp),
		_mRequestURI:        text("a URL", match.RequestURI, checkRequestURI),
		_mHasKey:            text("a JSON path", match.HasKey, checkJSONPath),
		_mEqualBytes:        text("standard base64", equalBytes, checkBase64),

		_mGreater:            order(match.GreaterThan[int64], match.GreaterThan[float64]),
		_mGreaterThanOrEqual: order(match.GreaterThanOrEqual[int64], match.GreaterThanOrEqual[float64]),
		_mLessThan:           order(match.LessThan[int64], match.LessThan[float64]),
		_mLessThanOrEqual:    order(match.LessThanOrEqual[int64], match.LessThanOrEqual[float64]),
		_mLen:                spec.length,

		_mEach:      inner(match.Each),
		_mSome:      inner(match.Some),
		_mNot:       inner(match.Not),
		_mTrim:      inner(match.Trim),
		_mLowerCase: inner(match.ToLower),
		_mUpperCase: inner(match.ToUpper),
		_mURLPath:   spec.urlPath,

		_mItem:     spec.item,
		_mJSONPath: spec.jsonPath,
		_mSplit:    spec.split,

		_mBoth:   pair(match.Both),
		_mEither: pair(match.Either),
		_mXOR:    pair(match.XOR),
		_mAll:    many(match.All),
		_mAny:    many(match.Any),
	}

	for alias, name := range map[string]string{
		_mEqualToAlias:            _mEqualTo,
		_mEqualToStrictAlias:      _mEqualToStrict,
		_mEqualJSONAlias:          _mEqualJSON,
		_mEqualXMLAlias:           _mEqualXML,
		_mEqualDateTimeAlias:      _mEqualDateTime,
		_mEqualToIgnoreCaseAlias:  _mEqualToIgnoreCase,
		_mRegexAlias:              _mRegex,
		_mGreaterAlias:            _mGreater,
		_mGreaterThanOrEqualAlias: _mGreaterThanOrEqual,
		_mLessThanAlias:           _mLessThan,
		_mLessThanOrEqualAlias:    _mLessThanOrEqual,
		_mLength:                  _mLen,
		_mField:                   _mJSONPath,
	} {
		builtins[alias] = builtins[name]
	}

	return builtins
}

// spec is a matcher's name and arguments, as written, for checks whose errors name the matcher.
type spec struct {
	name     string
	args     []any
	registry *Registry // builds the matchers of nested specs
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

// arityWithOptions checks that the spec has n arguments, or n and an object of options.
func (s spec) arityWithOptions(n int) error {
	if len(s.args) == n || len(s.args) == n+1 {
		return nil
	}

	return fmt.Errorf("%s takes %s, or %d with options, not %d", s.name, plural(n, "argument"), n+1, len(s.args))
}

// options reads the argument at i, when the spec has one, as an object of options, each key one of keys.
func (s spec) options(i int, keys ...string) (map[string]any, error) {
	if len(s.args) <= i {
		return nil, nil
	}

	opts, ok := s.args[i].(map[string]any)
	if !ok {
		return nil, s.takes("options, an object such as {"+keys[0]+": ...}", s.args[i], "")
	}

	for _, key := range slices.Sorted(maps.Keys(opts)) {
		if !slices.Contains(keys, key) {
			return nil, fmt.Errorf("%s takes the options %s, not %q", s.name, strings.Join(keys, ", "), key)
		}
	}

	return opts, nil
}

// flag reads the option key of opts, true or false. A missing option is false.
func (s spec) flag(opts map[string]any, key string) (bool, error) {
	v, ok := opts[key]
	if !ok {
		return false, nil
	}

	on, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s takes true or false for %s, not %s", s.name, key, config.Describe(v))
	}

	return on, nil
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
	if err := s.arityWithOptions(1); err != nil {
		return nil, err
	}

	opts, err := s.options(1, _oIgnoreArrayOrder, _oIgnoreExtraElements)
	if err != nil {
		return nil, err
	}

	var jsonOpts []match.EqualJSONOption

	if on, err := s.flag(opts, _oIgnoreArrayOrder); err != nil {
		return nil, err
	} else if on {
		jsonOpts = append(jsonOpts, match.IgnoreArrayOrder())
	}

	if on, err := s.flag(opts, _oIgnoreExtraElements); err != nil {
		return nil, err
	} else if on {
		jsonOpts = append(jsonOpts, match.IgnoreExtraElements())
	}

	if text, ok := s.args[0].(string); ok {
		_, err = value.DecodeJSON([]byte(text))
	} else {
		_, err = value.ViaJSON(s.args[0])
	}

	if err != nil {
		return nil, fmt.Errorf("%s takes JSON: %w", s.name, err)
	}

	return match.EqualJSON(s.args[0], jsonOpts...), nil
}

// jsonSchema builds ["jsonschema", schema] and ["jsonschema", schema, {draft: "7"}]. The schema is JSON text, or
// written as JSON.
func (s spec) jsonSchema() (vlatte.Matcher, error) {
	if err := s.arityWithOptions(1); err != nil {
		return nil, err
	}

	opts, err := s.options(1, _oDraft)
	if err != nil {
		return nil, err
	}

	var schemaOpts []match.JSONSchemaOption

	if draft, ok := opts[_oDraft]; ok {
		name := fmt.Sprint(draft)
		d, known := _drafts[name]
		if _, isText := draft.(string); !known || !isText && !isWhole(draft) {
			return nil, fmt.Errorf("%s takes the draft 4, 6, 7, 2019-09 or 2020-12, not %s", s.name, config.Describe(draft))
		}

		schemaOpts = append(schemaOpts, match.SchemaDraft(d))
	}

	schema, isText := s.args[0].(string)
	if !isText {
		data, err := json.Marshal(s.args[0], json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("%s takes a JSON Schema: %w", s.name, err)
		}

		schema = string(data)
	}

	m, err := built(func() vlatte.Matcher { return match.JSONSchema(schema, schemaOpts...) })
	if err != nil {
		return nil, fmt.Errorf("%s takes a JSON Schema: %w", s.name, err)
	}

	return m, nil
}

// xml builds ["equalxml", xml] and ["equalxml", xml, {ignore_child_order: true, namespaces: strict}].
func (s spec) xml() (vlatte.Matcher, error) {
	if err := s.arityWithOptions(1); err != nil {
		return nil, err
	}

	doc, ok := s.args[0].(string)
	if !ok {
		return nil, s.takes("an XML document", s.args[0], "")
	}

	opts, err := s.options(1, _oIgnoreChildOrder, _oNamespaces)
	if err != nil {
		return nil, err
	}

	var xmlOpts []match.EqualXMLOption

	if on, err := s.flag(opts, _oIgnoreChildOrder); err != nil {
		return nil, err
	} else if on {
		xmlOpts = append(xmlOpts, match.IgnoreChildOrder())
	}

	if v, ok := opts[_oNamespaces]; ok {
		name, _ := v.(string)
		mode, known := _namespaceModes[name]
		if !known {
			return nil, fmt.Errorf("%s takes the namespaces uri, strict or none, not %s", s.name, describeArg(v))
		}

		xmlOpts = append(xmlOpts, match.XMLNamespaces(mode))
	}

	m, err := built(func() vlatte.Matcher { return match.EqualXML(doc, xmlOpts...) })
	if err != nil {
		return nil, fmt.Errorf("%s takes an XML document: %w", s.name, err)
	}

	return m, nil
}

// xpath builds ["xpath", expression, matcher] and ["xpath", expression, matcher, {namespaces: {prefix: uri}}].
func (s spec) xpath() (vlatte.Matcher, error) {
	if err := s.arityWithOptions(2); err != nil {
		return nil, err
	}

	expr, ok := s.args[0].(string)
	if !ok {
		return nil, s.takes("an XPath expression first", s.args[0], "")
	}

	opts, err := s.options(2, _oNamespaces)
	if err != nil {
		return nil, err
	}

	var xpathOpts []match.XPathOption

	if v, ok := opts[_oNamespaces]; ok {
		namespaces, isObject := v.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("%s takes namespaces by prefix, such as {p: \"urn:p\"}, not %s", s.name, config.Describe(v))
		}

		for _, prefix := range slices.Sorted(maps.Keys(namespaces)) {
			uri, isText := namespaces[prefix].(string)
			if !isText {
				return nil, fmt.Errorf("%s takes a namespace URI for %s, not %s", s.name, prefix, config.Describe(namespaces[prefix]))
			}

			xpathOpts = append(xpathOpts, match.XPathNamespace(prefix, uri))
		}
	}

	inner, err := s.matcher(1)
	if err != nil {
		return nil, err
	}

	m, err := built(func() vlatte.Matcher { return match.XPath(expr, inner, xpathOpts...) })
	if err != nil {
		return nil, fmt.Errorf("%s takes an XPath expression first: %w", s.name, err)
	}

	return m, nil
}

// dateTime returns the builder of ["before", "now +3 days"], ["before", date and time] and the like, with options:
// {truncate_expected: "first day of month", truncate_actual: ..., truncate_last: true, actual_format: "02/01/2006",
// local_time: true}. A date and time without a zone is local. at builds the matcher of a date and time; now, of now.
func dateTime(
	at func(time.Time, ...match.DateTimeOption) vlatte.Matcher,
	now func(...match.DateTimeOption) vlatte.Matcher,
) builtin {
	return func(s spec) (vlatte.Matcher, error) {
		if err := s.arityWithOptions(1); err != nil {
			return nil, err
		}

		text, ok := s.args[0].(string)
		if !ok {
			return nil, s.takes(`a date and time, or now, such as "now +3 days"`, s.args[0], "")
		}

		opts, err := s.dateTimeOptions()
		if err != nil {
			return nil, err
		}

		base, n, unitName, hasOffset := timetext.CutOffset(strings.TrimSpace(text))
		if hasOffset {
			unit, known := _units[strings.TrimSuffix(strings.ToLower(unitName), "s")+"s"]
			if !known {
				return nil, fmt.Errorf("%s takes an offset in seconds, minutes, hours, days, weeks, months or years, not %q",
					s.name, unitName)
			}

			opts = append(opts, match.ExpectedOffset(n, unit))
		}

		if strings.EqualFold(base, "now") {
			return now(opts...), nil
		}

		t, zoned, err := timetext.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("%s takes a date and time, or now: %w", s.name, err)
		}

		if !zoned {
			opts = append(opts, match.LocalTime())
		}

		return at(t, opts...), nil
	}
}

// dateTimeOptions reads the options of a date and time matcher.
func (s spec) dateTimeOptions() ([]match.DateTimeOption, error) {
	opts, err := s.options(1, _oTruncateExpected, _oTruncateActual, _oTruncateLast, _oActualFormat, _oLocalTime)
	if err != nil {
		return nil, err
	}

	var dateOpts []match.DateTimeOption

	for _, t := range []struct {
		key    string
		option func(match.Truncation) match.DateTimeOption
	}{
		{key: _oTruncateExpected, option: match.TruncateExpected},
		{key: _oTruncateActual, option: match.TruncateActual},
	} {
		key := t.key

		v, ok := opts[key]
		if !ok {
			continue
		}

		name, _ := v.(string)
		truncation, known := _truncations[strings.ToLower(name)]
		if !known {
			return nil, fmt.Errorf("%s takes a truncation for %s, such as \"first day of month\", not %s", s.name, key, describeArg(v))
		}

		dateOpts = append(dateOpts, t.option(truncation))
	}

	if on, err := s.flag(opts, _oTruncateLast); err != nil {
		return nil, err
	} else if on {
		dateOpts = append(dateOpts, match.TruncateLast())
	}

	if on, err := s.flag(opts, _oLocalTime); err != nil {
		return nil, err
	} else if on {
		dateOpts = append(dateOpts, match.LocalTime())
	}

	if v, ok := opts[_oActualFormat]; ok {
		layout, isText := v.(string)
		if !isText || layout == "" {
			return nil, fmt.Errorf("%s takes a Go time layout for %s, such as \"02/01/2006\", not %s", s.name, _oActualFormat, describeArg(v))
		}

		dateOpts = append(dateOpts, match.ActualFormat(layout))
	}

	return dateOpts, nil
}

// describeArg describes an argument for an error: text in quotes, and anything else by its kind.
func describeArg(v any) string {
	if text, ok := v.(string); ok {
		return strconv.Quote(text)
	}

	return config.Describe(v)
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
	m, err := s.registry.BuildMatcher(s.args[i])
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

// length builds ["len", n].
func (s spec) length() (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	n, err := s.count(0, "a length")
	if err != nil {
		return nil, err
	}

	return match.Len(n), nil
}

// urlPath builds ["urlpath", pattern], which globs the path, or ["urlpath", matcher], as descriptions write it.
func (s spec) urlPath() (vlatte.Matcher, error) {
	if err := s.arity(1); err != nil {
		return nil, err
	}

	if pattern, ok := s.args[0].(string); ok {
		return match.URLPath(pattern), nil
	}

	return s.inner(match.URLPathMatch)
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

		m, err := s.registry.BuildMatcher(entry)
		if err != nil {
			return nil, fmt.Errorf("%s: matcher %d: %w", s.name, i, err)
		}

		matchers = append(matchers, m)
	}

	return matchers, nil
}

// hasExactly builds ["hasexactly", [matcher, ...]]: a list of as many items as matchers, each matched by a matcher
// of its own, in any order.
func (s spec) hasExactly() (vlatte.Matcher, error) {
	if len(s.args) == 0 {
		return nil, s.arity(1)
	}

	matchers, err := s.matchers()
	if err != nil {
		return nil, err
	}

	return match.ItemsMatch(matchers), nil
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

// built returns what build, a constructor of package match, returns, or the error it panics with on input it can't
// build: match checks such input only by building it.
func built(build func() vlatte.Matcher) (m vlatte.Matcher, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		cause, isError := r.(error)
		if _, isRuntime := r.(runtime.Error); !isError || isRuntime {
			panic(r)
		}

		err = cause
	}()

	return build(), nil
}

// isWhole reports whether v is a whole number, as a mock file gives one.
func isWhole(v any) bool {
	_, ok := v.(int64)
	return ok
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
