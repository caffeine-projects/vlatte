package match

import (
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match/internal/anyconv"
)

var (
	_ vlatte.Matcher         = (*jsonSchemaMatcher)(nil)
	_ vlatte.SelfDescriptive = (*jsonSchemaMatcher)(nil)
)

// jsonSchemaMatcher passes when the JSON of the value conforms to the schema.
type jsonSchemaMatcher struct {
	Schema   string
	draft    JSONSchemaDraft
	drafted  bool // whether SchemaDraft set the draft, which the description then writes
	compiled *jsonschema.Schema
}

// Name implements vlatte.Matcher.
func (m *jsonSchemaMatcher) Name() string { return "JSONSchema" }

// Match implements vlatte.Matcher.
func (m *jsonSchemaMatcher) Match(arg vlatte.MatcherArg) (bool, error) {
	v := arg.Value()

	doc, err := jsonOf(v)
	if err == nil && m.compiled.Validate(doc) == nil {
		return success()
	}

	return mismatch(strings.Join([]string{"JSONSchema() Got: ", anyconv.Stringify(v)}, ""))
}

// Describe implements vlatte.SelfDescriptive.
func (m *jsonSchemaMatcher) Describe() vlatte.Descriptor {
	if !m.drafted {
		return vlatte.DescribeSingleArg("jsonSchema", m.Schema)
	}

	return vlatte.DescribeMultiArgs("jsonSchema", m.Schema, map[string]any{"draft": m.draft.String()})
}
