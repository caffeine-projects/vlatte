package match

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/caffeine-projects/vlatte"
)

// JSONSchemaDraft is a version of the JSON Schema specification.
type JSONSchemaDraft int

// The versions of JSON Schema that [SchemaDraft] takes.
const (
	JSONSchemaDraft4    JSONSchemaDraft = iota + 1 // draft-04
	JSONSchemaDraft6                               // draft-06
	JSONSchemaDraft7                               // draft-07
	JSONSchemaDraft2019                            // 2019-09
	JSONSchemaDraft2020                            // 2020-12
)

// String returns the name of the draft as mock files write it: 4, 6, 7, 2019-09 or 2020-12.
func (d JSONSchemaDraft) String() string {
	switch d {
	case JSONSchemaDraft4:
		return "4"
	case JSONSchemaDraft6:
		return "6"
	case JSONSchemaDraft7:
		return "7"
	case JSONSchemaDraft2019:
		return "2019-09"
	case JSONSchemaDraft2020:
		return "2020-12"
	default:
		return fmt.Sprintf("JSONSchemaDraft(%d)", int(d))
	}
}

func (d JSONSchemaDraft) spec() (*jsonschema.Draft, bool) {
	switch d {
	case JSONSchemaDraft4:
		return jsonschema.Draft4, true
	case JSONSchemaDraft6:
		return jsonschema.Draft6, true
	case JSONSchemaDraft7:
		return jsonschema.Draft7, true
	case JSONSchemaDraft2019:
		return jsonschema.Draft2019, true
	case JSONSchemaDraft2020:
		return jsonschema.Draft2020, true
	default:
		return nil, false
	}
}

// JSONSchema matches a value whose JSON conforms to schema, a JSON Schema written as JSON text. A value that is text,
// such as a request body, is read as JSON; any other value is written as JSON. Formats, such as "email", are checked.
//
// The schema is of the draft its $schema names, or else of 2020-12, unless [SchemaDraft] says otherwise. It loads
// nothing else: a $ref to anything outside the schema fails, so a mock file can't make the server read files or the
// network. JSONSchema panics when schema isn't a JSON Schema.
func JSONSchema(schema string, opts ...JSONSchemaOption) vlatte.Matcher {
	o := schemaOptions{draft: JSONSchemaDraft2020}
	for _, opt := range opts {
		opt.applySchema(&o)
	}

	compiled, err := compileSchema(schema, o.draft)
	if err != nil {
		panic(fmt.Errorf("match: jsonschema: %w", err))
	}

	return &jsonSchemaMatcher{Schema: schema, draft: o.draft, drafted: o.drafted, compiled: compiled}
}

// JSONSchemaOption configures [JSONSchema].
type JSONSchemaOption interface {
	applySchema(o *schemaOptions)
}

type schemaOptions struct {
	draft   JSONSchemaDraft
	drafted bool // whether SchemaDraft set the draft
}

type schemaDraft JSONSchemaDraft

func (d schemaDraft) applySchema(o *schemaOptions) { o.draft, o.drafted = JSONSchemaDraft(d), true }

// SchemaDraft sets the draft of a schema of [JSONSchema] without $schema.
func SchemaDraft(d JSONSchemaDraft) JSONSchemaOption { return schemaDraft(d) }

// _schemaURL is where a schema of JSONSchema is, for its compiler: a name, which nothing loads from.
const _schemaURL = "mem://vlatte/matcher.schema.json"

var errSchemaLoad = errors.New("loads nothing outside the schema")

// noLoader is the loader of a schema of JSONSchema: it loads nothing.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("%s: %w", url, errSchemaLoad)
}

func compileSchema(schema string, draft JSONSchemaDraft) (*jsonschema.Schema, error) {
	spec, ok := draft.spec()
	if !ok {
		return nil, fmt.Errorf("unknown draft %s", draft)
	}

	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
	if err != nil {
		return nil, err
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(spec)
	c.AssertFormat()
	c.UseLoader(noLoader{})

	if err := c.AddResource(_schemaURL, doc); err != nil {
		return nil, err
	}

	return c.Compile(_schemaURL)
}
