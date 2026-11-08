// Package openapiv3 reads OpenAPI 3.0, 3.1 and 3.2 documents into an [openapi.Document], with kin-openapi.
package openapiv3

import (
	"cmp"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/rs/zerolog"

	"github.com/caffeine-projects/vlatte/internal/openapi"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// _standInValue is the externalValue an example holds while kin-openapi validates it, when kin-openapi reads it as
// having no value: one whose value is null, or one of OpenAPI 3.2 with only a dataValue or a serializedValue.
const _standInValue = "vlatte:example"

// _openAPI32Fields are the fields OpenAPI 3.2 adds that kin-openapi doesn't read and would reject as unknown: those of
// the document, components, encodings, examples, media types, OAuth flows, responses, security schemes, servers and
// tags. kin-openapi allows them by name, on any object. No mock needs what they say but for the values of examples.
var _openAPI32Fields = []string{
	"$self", "dataValue", "deprecated", "deviceAuthorization", "encoding", "itemEncoding", "kind", "mediaTypes", "name",
	"oauth2MetadataUrl", "parent", "prefixEncoding", "serializedValue", "summary",
}

// _componentsPrefix starts a reference to a shared schema, which the model writes under $defs.
const _componentsPrefix = "#/components/schemas/"

// Load reads data, the OpenAPI 3 document at location, and the files its references name, which read returns. It
// logs on log, at warn level, what it reads but cannot honor, such as servers with different paths, and each example
// that doesn't fit its schema: [zerolog.Nop] silences it. Such an example is converted when the text of each value
// that breaks it reads as the type its schema asks, such as 12345 for a string, else left out. It fails with
// [openapi.ErrInvalid] for a document kin-openapi finds invalid but for its examples, or with a null where an object
// belongs.
func Load(ctx context.Context, data []byte, location *url.URL, read func(u *url.URL) ([]byte, error), log *zerolog.Logger) (*openapi.Document, error) {
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	// The files kin-openapi reads, kept for their examples as written.
	files := map[string][]byte{fileKey(location): data}
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
		b, err := read(u)
		if err == nil {
			files[fileKey(u)] = b
		}

		return b, err
	}

	t, err := loader.LoadFromDataWithPath(data, location)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", openapi.ErrInvalid, err)
	}

	r, where, serverFiles := readRaw(t, location, files)

	if err := validate(ctx, t, r, where, log); err != nil {
		return nil, fmt.Errorf("%w: %w", openapi.ErrInvalid, err)
	}

	// The model refers to shared schemas by name: a reference to another file becomes one.
	t.InternalizeRefs(ctx, refNames(location))

	c := converter{v30: t.IsOpenAPI30(), v32: t.IsOpenAPI32OrLater(), raw: r, places: where, log: log, fits: make(map[fitKey]fitted)}
	doc := &openapi.Document{Defs: make(map[string]any)}

	if t.Info != nil {
		doc.Title = t.Info.Title
	}

	served := servedFrom(location, serverFiles)
	root := basePath(t.Servers, served, log)

	if t.Components != nil {
		// Sorted, as the paths are.
		for _, name := range slices.Sorted(maps.Keys(t.Components.Schemas)) {
			doc.Defs[name] = c.schema(t.Components.Schemas[name])
		}
	}

	if t.Paths == nil {
		return doc, nil
	}

	// Sorted, so the warnings are logged in the same order on every run.
	paths := t.Paths.Map()
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		item := paths[path]

		// A path's servers replace the document's, and an operation's replace its path's.
		itemBase := root
		if len(item.Servers) > 0 {
			pathLog := log.With().Str("path", path).Logger()
			itemBase = basePath(item.Servers, served, &pathLog)
		}

		ops := item.Operations()
		for _, method := range slices.Sorted(maps.Keys(ops)) {
			op := ops[method]
			o := c.operation(path, strings.ToUpper(method), item.Parameters, op)
			o.BasePath = itemBase

			if op.Servers != nil && len(*op.Servers) > 0 {
				opLog := log.With().Str("method", o.Method).Str("path", path).Logger()
				o.BasePath = basePath(*op.Servers, served, &opLog)
			}

			doc.Operations = append(doc.Operations, o)
		}
	}

	return doc, nil
}

// refNames returns how kin-openapi names, as a shared component, what a reference of the spec at location names
// outside its components: by its file, relative to location's folder, without its extension, and its JSON pointer,
// without its components part: pets.yaml#/components/schemas/Pet is pets_Pet. A name taken by another component gets
// a number. kin-openapi's own naming loops forever on a reference into paths.
func refNames(location *url.URL) openapi3.RefNameResolver {
	given := make(map[string]string) // the reference each name was given to, by collection and name

	return func(t *openapi3.T, ref openapi3.ComponentRef) string {
		target, collection := ref.RefPath(), ref.CollectionName()
		local := fileKey(target) == fileKey(location)

		pointer := strings.TrimPrefix(target.Fragment, "/")
		if name, ok := strings.CutPrefix(pointer, "components/"+collection+"/"); ok {
			if local && !strings.Contains(name, "/") {
				return name
			}

			pointer = name
		}

		file := ""
		if !local {
			file = relative(location, target)
			file = strings.TrimSuffix(file, path.Ext(file))
		}

		base := openapi3.InvalidIdentifierCharRegExp.ReplaceAllString(strings.Trim(file+"_"+pointer, "_/"), "_")

		name := base
		for i := 2; ; i++ {
			key := collection + "/" + name

			prev, ok := given[key]
			if ok && prev == target.String() {
				return name
			}

			if !ok && !hasComponent(t, collection, name) {
				given[key] = target.String()
				return name
			}

			name = base + "_" + strconv.Itoa(i)
		}
	}
}

// relative returns the path of u, a file of the spec at location, relative to location's folder: pets.yaml.
func relative(location, u *url.URL) string {
	return strings.TrimPrefix(u.Path, path.Dir(location.Path)+"/")
}

// hasComponent reports whether t has a component name in collection, such as schemas.
func hasComponent(t *openapi3.T, collection, name string) bool {
	if t.Components == nil {
		return false
	}

	m := reflect.ValueOf(t.Components).Elem().FieldByNameFunc(func(field string) bool { return strings.EqualFold(field, collection) })

	return m.Kind() == reflect.Map && m.MapIndex(reflect.ValueOf(name)).IsValid()
}

// validate validates t as [lenient] does, but for the values of examples, which kin-openapi would reject the
// document for: the converter checks those the mocks use, as [converter.fit] does. The named examples of media types
// and parameters are checked otherwise as kin-openapi does, but for a field OpenAPI doesn't define, which is logged on
// log. A default r says is null, which kin-openapi reads as no value, is checked as null. In OpenAPI 3.2, the fields it
// adds that kin-openapi doesn't read are no news, and those of an example that hold its value are checked as OpenAPI
// 3.2 has them. The walk meets the parts of t in the same order on every run, which names the same error.
func validate(ctx context.Context, t *openapi3.T, r raw, where *places, log *zerolog.Logger) error {
	opts := []openapi3.ValidationOption{openapi3.DisableExamplesValidation()}
	if t.IsOpenAPI32OrLater() {
		opts = append(opts, openapi3.AllowExtraSiblingFields(_openAPI32Fields...))
	}

	// A media type of a shared component is reached from each of its users: it is walked once. A callback may lead
	// back to a path item already walked.
	seen := make(map[*openapi3.MediaType]bool)
	walked := make(map[*openapi3.PathItem]bool)

	var (
		named   []openapi3.Examples // the named examples kin-openapi checks with their values, as the walk meets them
		content func(c openapi3.Content)
		invalid error
	)

	// kin-openapi loads and validates a null where some objects belong: null records the first, what, when isNull,
	// and returns isNull, for the walk to skip it.
	null := func(isNull bool, what string) bool {
		if isNull && invalid == nil {
			invalid = fmt.Errorf("%s: expected an object, not null", what)
		}

		return isNull
	}

	// kin-openapi resolves every reference it loads, or fails: a reference holds its value.
	header := func(h *openapi3.HeaderRef) { content(h.Value.Content) }

	content = func(c openapi3.Content) {
		for _, name := range slices.Sorted(maps.Keys(c)) {
			mt := c[name]
			if null(mt == nil, "media type "+name) || seen[mt] {
				continue
			}

			seen[mt] = true

			for _, field := range slices.Sorted(maps.Keys(mt.Encoding)) {
				e := mt.Encoding[field]
				if null(e == nil, "encoding "+field) {
					continue
				}

				for _, h := range inOrder(e.Headers) {
					header(h)
				}
			}

			if mt.Schema != nil {
				named = append(named, mt.Examples)
			}
		}
	}

	parameter := func(p *openapi3.ParameterRef) {
		content(p.Value.Content)

		if p.Value.Schema != nil {
			named = append(named, p.Value.Examples)
		}
	}

	response := func(r *openapi3.ResponseRef) {
		content(r.Value.Content)

		for _, h := range inOrder(r.Value.Headers) {
			header(h)
		}
	}

	// kin-openapi checks the document's servers only: those of a path or an operation are checked the same way.
	servers := func(ss openapi3.Servers, what string) {
		if err := ss.Validate(ctx, opts...); err != nil && invalid == nil {
			invalid = fmt.Errorf("invalid %s: invalid servers: %w", what, err)
		}
	}

	var item func(name string, p *openapi3.PathItem)

	callbacks := func(cs openapi3.Callbacks) {
		for _, c := range inOrder(cs) {
			for _, name := range slices.Sorted(maps.Keys(c.Value.Map())) {
				item("callback "+name, c.Value.Value(name))
			}
		}
	}

	item = func(name string, p *openapi3.PathItem) {
		if null(p == nil, name) || walked[p] {
			return
		}

		walked[p] = true

		servers(p.Servers, name)

		for _, param := range p.Parameters {
			parameter(param)
		}

		ops := p.Operations()
		for _, method := range slices.Sorted(maps.Keys(ops)) {
			op := ops[method]
			if op.Servers != nil {
				servers(*op.Servers, name+": invalid operation "+method)
			}

			for _, param := range op.Parameters {
				parameter(param)
			}

			if op.RequestBody != nil {
				content(op.RequestBody.Value.Content)
			}

			if op.Responses != nil {
				for _, r := range inOrder(op.Responses.Map()) {
					response(r)
				}
			}

			callbacks(op.Callbacks)
		}
	}

	if t.Paths != nil {
		for _, name := range slices.Sorted(maps.Keys(t.Paths.Map())) {
			item("path "+name, t.Paths.Value(name))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(t.Webhooks)) {
		item("webhook "+name, t.Webhooks[name])
	}

	if c := t.Components; c != nil {
		for _, p := range inOrder(c.Parameters) {
			parameter(p)
		}

		for _, h := range inOrder(c.Headers) {
			header(h)
		}

		for _, b := range inOrder(c.RequestBodies) {
			content(b.Value.Content)
		}

		for _, r := range inOrder(c.Responses) {
			response(r)
		}

		callbacks(c.Callbacks)
	}

	if invalid != nil {
		return invalid
	}

	examples := make(map[*openapi3.Example]bool)

	for f := range r {
		if ex, ok := f.object.(*openapi3.Example); ok {
			examples[ex] = true
		}
	}

	if t.IsOpenAPI32OrLater() {
		for _, ex := range slices.SortedFunc(maps.Keys(examples), func(a, b *openapi3.Example) int {
			return cmp.Compare(where.of(field{object: a, name: "value"}), where.of(field{object: b, name: "value"}))
		}) {
			if err := checkValues(r, ex); err != nil {
				return fmt.Errorf("invalid example %s: %w", where.of(field{object: ex, name: "value"}), err)
			}
		}
	}

	// kin-openapi rejects an example with neither a value nor an externalValue, as it reads one whose value is null,
	// and one of OpenAPI 3.2 with only a dataValue or a serializedValue, which it doesn't know: such an example stands
	// in as one with an externalValue.
	var standIns []*openapi3.Example

	for ex := range examples {
		if ex.Value == nil && ex.ExternalValue == "" {
			ex.ExternalValue = _standInValue
			standIns = append(standIns, ex)
		}
	}

	defer func() {
		for _, ex := range standIns {
			ex.ExternalValue = ""
		}
	}()

	if err := lenient(ctx, t, log); err != nil {
		return err
	}

	for _, examples := range named {
		for _, name := range slices.Sorted(maps.Keys(examples)) {
			ex := examples[name].Value
			at := cmp.Or(where.of(field{object: ex, name: "value"}), name)

			// kin-openapi checks the fields of an example after its value: one OpenAPI doesn't define is logged, as
			// lenient logs one elsewhere.
			switch err := ex.Validate(ctx, opts...); {
			case err == nil:
			case is[*openapi3.ExtraSiblingFieldsError](err):
				log.Warn().Str("example", at).Msgf("%v: ignored", err)
			default:
				return fmt.Errorf("invalid example %s: %w", at, err)
			}
		}
	}

	return validateDefaults(t, r, where)
}

// checkValues checks the fields of example ex that hold its value, as r has them, as OpenAPI 3.2 has them, which
// kin-openapi doesn't know: value excludes dataValue and serializedValue, and externalValue excludes serializedValue,
// which is text.
func checkValues(r raw, ex *openapi3.Example) error {
	_, value := r[field{object: ex, name: "value"}]
	_, data := r[field{object: ex, name: "dataValue"}]
	serialized, isSerialized := r[field{object: ex, name: "serializedValue"}]

	switch {
	case value && data:
		return errors.New("value and dataValue are mutually exclusive")
	case value && isSerialized:
		return errors.New("value and serializedValue are mutually exclusive")
	case ex.ExternalValue != "" && isSerialized:
		return errors.New("externalValue and serializedValue are mutually exclusive")
	}

	if _, ok := serialized.(string); isSerialized && !ok {
		return fmt.Errorf("serializedValue: expected a string, not %s", text(serialized))
	}

	return nil
}

// inOrder returns the values of m in the order of their keys.
func inOrder[M ~map[string]V, V any](m M) []V {
	out := make([]V, 0, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, m[k])
	}

	return out
}

// lenient validates t as kin-openapi does, but for the values of examples, and returns the first problem that would
// make a mock wrong. It logs the others on log, at warn level: a field OpenAPI doesn't define, which the mocks ignore,
// but for those OpenAPI 3.2 adds, in a 3.2 document, and two paths that differ only in the names of their parameters,
// whose mocks of one method answer the same requests. It doesn't compile patterns: a pattern is an ECMA-262 regular
// expression, such as a lookahead, which Go's regexp may not compile.
func lenient(ctx context.Context, t *openapi3.T, log *zerolog.Logger) error {
	var allowed []string
	if t.IsOpenAPI32OrLater() {
		allowed = slices.Clone(_openAPI32Fields)
	}

	for {
		opts := []openapi3.ValidationOption{
			openapi3.EnableMultiError(), openapi3.DisableSchemaPatternValidation(), openapi3.DisableExamplesValidation(),
			openapi3.AllowExtraSiblingFields(allowed...),
		}

		if t.IsOpenAPI31OrLater() {
			opts = append(opts, openapi3.IsOpenAPI31OrLater())
		}

		if t.IsOpenAPI32OrLater() {
			opts = append(opts, openapi3.IsOpenAPI32OrLater())
		}

		problems := flatten(t.Validate(ctx, opts...))

		// kin-openapi skips a path that conflicts with another: it is checked on its own.
		for i := 0; i < len(problems); i++ {
			if c, ok := errors.AsType[*openapi3.ConflictingPathsError](problems[i]); ok {
				skipped := openapi3.NewPaths(openapi3.WithPath(c.Path1, t.Paths.Value(c.Path1)))
				problems = append(problems, flatten(skipped.Validate(ctx, opts...))...)
			}
		}

		// kin-openapi checks no more of a schema past an unknown field: a field allowed, it checks again.
		more := len(allowed)

		for _, p := range problems {
			if e, ok := errors.AsType[*openapi3.ExtraSiblingFieldsError](p); ok {
				log.Warn().Msgf("%v: ignored", p)

				for _, f := range e.Fields {
					if !slices.Contains(allowed, f) {
						allowed = append(allowed, f)
					}
				}
			}
		}

		if len(allowed) > more {
			continue
		}

		for _, p := range problems {
			switch {
			case p == nil:
			case is[*openapi3.ConflictingPathsError](p):
				log.Warn().Msgf("%v: their mocks of one method answer the same requests", p)
			default:
				return p
			}
		}

		return nil
	}
}

// flatten returns the problems of err, the error of a validation in kin-openapi's multi-error mode, or err, which may
// be nil.
func flatten(err error) []error {
	problems := []error{err}
	if multi, ok := errors.AsType[openapi3.MultiError](err); ok {
		problems = multi
	}

	return problems
}

// is reports whether err, or an error it wraps, is a T.
func is[T error](err error) bool {
	_, ok := errors.AsType[T](err)
	return ok
}

// validateDefaults checks each default r says is null, which kin-openapi reads as no value, against its schema, as
// kin-openapi checks one that isn't, in the order of where they are.
func validateDefaults(t *openapi3.T, r raw, where *places) error {
	opts := []openapi3.SchemaValidationOption{openapi3.MultiErrors()}
	if t.IsOpenAPI31OrLater() {
		opts = append(opts, openapi3.EnableJSONSchema2020())
	}

	var nulls []field

	for f, v := range r {
		if _, ok := f.object.(*openapi3.Schema); ok && f.name == "default" && v == nil {
			nulls = append(nulls, f)
		}
	}

	slices.SortFunc(nulls, func(a, b field) int { return cmp.Compare(where.of(a), where.of(b)) })

	for _, f := range nulls {
		s, _ := f.object.(*openapi3.Schema)
		if err := t.ValidateSchemaJSON(s, nil, opts...); err != nil {
			return fmt.Errorf("invalid default of %s: %w", where.of(f), err)
		}
	}

	return nil
}

// servedFrom returns the URL each server of the spec at location is relative to: that of the file it is written in,
// which files has for the servers of paths and operations, else location. A spec read from disk is served from the
// root, its folder as /, which a file outside it is under too: ../shared/e.yaml is /shared/e.yaml.
func servedFrom(location *url.URL, files map[*openapi3.Server]*url.URL) func(s *openapi3.Server) *url.URL {
	// A reference resolves to a path with a leading slash, a Windows path too: /C:/api/paths/b.yaml.
	folder := strings.Split(path.Dir(path.Join("/", location.Path)), "/")

	return func(s *openapi3.Server) *url.URL {
		file := cmp.Or(files[s], location)
		if location.IsAbs() {
			return file
		}

		segments := strings.Split(path.Join("/", file.Path), "/")

		common := 0
		for common < len(folder) && common < len(segments) && folder[common] == segments[common] {
			common++
		}

		up := slices.Repeat([]string{".."}, len(folder)-common)

		return &url.URL{Path: path.Join(slices.Concat([]string{"/"}, up, segments[common:])...)}
	}
}

// basePath returns the path of the first of servers, which the operations they serve are under. A server's URL, its
// variables set to their defaults, may be relative, as a link of the file it is written in, whose URL served returns:
// v1 is beside it, and //api.example.com/v1 is /v1. It logs on log a server whose URL it cannot read, and servers
// whose paths differ: the mocks answer the first one's only.
func basePath(servers openapi3.Servers, served func(s *openapi3.Server) *url.URL, log *zerolog.Logger) string {
	var paths []string

	for _, s := range servers {
		link := s.URL
		for _, name := range slices.Sorted(maps.Keys(s.Variables)) {
			link = strings.ReplaceAll(link, "{"+name+"}", s.Variables[name].Default)
		}

		u, err := url.Parse(link)
		if err != nil {
			log.Warn().Str("server", s.URL).Msgf("the server's path is not a URL: %v: ignored", err)
			continue
		}

		paths = append(paths, strings.TrimSuffix(served(s).ResolveReference(u).Path, "/"))
	}

	if len(paths) == 0 {
		return ""
	}

	if slices.ContainsFunc(paths, func(p string) bool { return p != paths[0] }) {
		log.Warn().Msgf("the servers have different paths: the mocks answer under the first one's, %q", cmp.Or(paths[0], "/"))
	}

	return paths[0]
}

// converter translates the parts of a document into the model.
type converter struct {
	v30    bool              // the document is OpenAPI 3.0, whose schemas are not JSON Schema 2020-12
	v32    bool              // the document is OpenAPI 3.2, whose examples may give their value as data or as sent
	raw    raw               // the examples as the spec writes them
	places *places           // where the fields of raw are, and the text of their scalars
	log    *zerolog.Logger   // where an example that doesn't fit its schema is logged
	fits   map[fitKey]fitted // what fit returned for each example that didn't fit, at a place the spec gives
}

// operation translates op, of the path item with the parameters common.
func (c converter) operation(path, method string, common openapi3.Parameters, op *openapi3.Operation) openapi.Operation {
	o := openapi.Operation{Method: method, Path: path, Summary: op.Summary}

	// An operation's parameter replaces the path item's of the same name and location.
	params := make(map[string]*openapi3.Parameter)
	for _, p := range slices.Concat(common, op.Parameters) {
		if p != nil && p.Value != nil && p.Value.In == openapi3.ParameterInPath {
			params[p.Value.Name] = p.Value
		}
	}

	for _, name := range slices.Sorted(maps.Keys(params)) {
		p := params[name]
		examples, schema, mediaType := c.examplesOf(p, p.Example, p.Examples, "", p.Schema, false), p.Schema, ""

		// A parameter has a schema or content, of one media type.
		if len(p.Content) > 0 {
			mediaType = slices.Min(slices.Collect(maps.Keys(p.Content)))
			media := p.Content[mediaType]
			examples = append(examples, c.examplesOf(media, media.Example, media.Examples, mediaType, media.Schema, false)...)
			schema = media.Schema
		}

		if s := schema; s != nil && s.Value != nil {
			examples = append(examples, c.examplesOf(s.Value, s.Value.Example, nil, "", s, false)...)

			var kin any
			if s.Value.Examples != nil {
				kin = s.Value.Examples
			}

			v, _ := c.raw.value(s.Value, "examples", kin)
			list, _ := v.([]any)

			for i, v := range list {
				if v, ok := c.fit(v, s, false, field{object: s.Value, name: "examples"}, "/"+strconv.Itoa(i)); ok {
					examples = append(examples, openapi.Example{Value: v})
				}
			}
		}

		o.PathParams = append(o.PathParams, openapi.Parameter{
			Name: name, Style: cmp.Or(p.Style, openapi3.SerializationSimple), Explode: p.Explode != nil && *p.Explode, MediaType: mediaType, Examples: examples,
		})
	}

	// Loaded, every response is resolved: Map of no responses is empty.
	responses := op.Responses.Map()
	for _, status := range slices.Sorted(maps.Keys(responses)) {
		o.Responses = append(o.Responses, c.response(status, responses[status].Value))
	}

	return o
}

// response translates the response of status.
func (c converter) response(status string, res *openapi3.Response) openapi.Response {
	r := openapi.Response{Status: strings.ToUpper(status)}
	if status == "default" {
		r.Status = status
	}

	for _, name := range slices.Sorted(maps.Keys(res.Headers)) {
		ref := res.Headers[name]
		if ref == nil || ref.Value == nil || strings.EqualFold(name, "Content-Type") {
			continue
		}

		h := openapi.Header{
			Name:     name,
			Explode:  ref.Value.Explode != nil && *ref.Value.Explode,
			Schema:   c.schema(ref.Value.Schema),
			Examples: c.examplesOf(ref.Value, ref.Value.Example, ref.Value.Examples, "", ref.Value.Schema, true),
		}

		// A header has a schema or content, of one media type.
		if len(ref.Value.Content) > 0 {
			mt := slices.Min(slices.Collect(maps.Keys(ref.Value.Content)))
			media := ref.Value.Content[mt]
			h.MediaType, h.Schema = mt, c.schema(media.Schema)
			h.Examples = c.examplesOf(media, media.Example, media.Examples, mt, media.Schema, true)
		}

		r.Headers = append(r.Headers, h)
	}

	for _, mt := range slices.Sorted(maps.Keys(res.Content)) {
		media := res.Content[mt]
		r.Content = append(r.Content, openapi.Content{
			MediaType: mt, Schema: c.schema(media.Schema), Examples: c.examplesOf(media, media.Example, media.Examples, mt, media.Schema, true),
		})
	}

	return r
}

// examplesOf returns the example of owner, the object whose example field is example, if it has one, then the named
// examples, in name order, with their values as the spec writes them. Those of mediaType, unless it is JSON, are the
// body as sent, such as text, which a named example of OpenAPI 3.2 gives as its serializedValue, else its dataValue;
// those of JSON, a parameter or a header, whose mediaType is "", are data, its dataValue, fitted to schema, as the
// body of a response when response is set, as fit does. A named example at an externalValue, not inline, is left
// out, as is one of data with only a serializedValue, which writes it as sent: a parameter with its style's
// delimiters, JSON as text. Before OpenAPI 3.2, those fields are unknown, and a named example has its value only.
func (c converter) examplesOf(owner, example any, named openapi3.Examples, mediaType string, schema *openapi3.SchemaRef, response bool) []openapi.Example {
	var out []openapi.Example

	values := []string{"value", "dataValue"}
	if mediaType != "" && !openapi.IsJSON(mediaType) {
		values, schema = []string{"value", "serializedValue", "dataValue"}, nil
	}

	if !c.v32 {
		values = values[:1]
	}

	f := field{object: owner, name: "example"}
	if v, ok := c.raw.value(owner, f.name, example); ok {
		if v, ok = c.fit(v, schema, response, f, ""); ok {
			out = append(out, openapi.Example{Value: v})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(named)) {
		// kin-openapi resolves every reference it loads, or fails: a reference holds its value.
		ex := named[name].Value

		// value excludes the others; text that is data is sent as is.
		for _, n := range values {
			kin := ex.Value
			if n != "value" {
				kin = nil
			}

			f := field{object: ex, name: n}
			if v, ok := c.raw.value(ex, f.name, kin); ok {
				if v, ok = c.fit(v, schema, response, f, ""); ok {
					out = append(out, openapi.Example{Name: name, Value: v})
				}

				break
			}
		}
	}

	return out
}

// schema translates the schema of ref into JSON Schema 2020-12, or returns nil for no schema. kin-openapi read the
// schema from JSON, which it writes back, with each integer as the spec writes it: a $ref as written, with the
// keywords beside it. Its examples are fitted to their schemas, as fitSchema does.
func (c converter) schema(ref *openapi3.SchemaRef) any {
	if ref == nil {
		return nil
	}

	data, _ := json.Marshal(ref)
	v, _ := value.DecodeJSON(data)

	switch {
	case ref.Ref != "":
		v = exact(v, c.raw[field{object: ref}])
	case ref.Value != nil:
		v = exact(v, c.raw[field{object: ref.Value}])
	}

	c.fitSchema(v, ref)

	return c.translate(v)
}

// translate rewrites schema v of the document as JSON Schema 2020-12: its references to shared schemas point under
// $defs, and, in OpenAPI 3.0, nullable adds the null type, and a boolean exclusiveMinimum or exclusiveMaximum takes
// the value of minimum or maximum.
func (c converter) translate(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch {
			case slices.Contains(openapi.ValueKeywords, k):
				out[k] = val
			case slices.Contains(openapi.SchemaMapKeywords, k):
				out[k] = c.translateEach(val)
			case k == "$ref":
				out[k] = localRef(val)
			case k == "discriminator":
				out[k] = discriminator(val)
			default:
				out[k] = c.translate(val)
			}
		}

		if c.v30 {
			translate30(out)
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = c.translate(item)
		}

		return out
	default:
		return v
	}
}

// translateEach translates each schema of a map of them, such as properties, whose keys are names, not keywords.
func (c converter) translateEach(v any) any {
	m, _ := v.(map[string]any)

	out := make(map[string]any, len(m))
	for k, s := range m {
		out[k] = c.translate(s)
	}

	return out
}

// localRef returns ref pointing under $defs when it points to a shared schema.
func localRef(ref any) any {
	s, _ := ref.(string)
	return strings.Replace(s, _componentsPrefix, openapi.DefsPrefix, 1)
}

// discriminator returns the discriminator v with the references of its mapping pointing under $defs.
func discriminator(v any) any {
	d, _ := v.(map[string]any)

	out := make(map[string]any, len(d))
	maps.Copy(out, d)

	if mapping, ok := d["mapping"].(map[string]any); ok {
		m := make(map[string]any, len(mapping))
		for k, ref := range mapping {
			m[k] = localRef(ref)
		}

		out["mapping"] = m
	}

	return out
}

// translate30 rewrites the OpenAPI 3.0 keywords of schema s that JSON Schema 2020-12 writes otherwise.
func translate30(s map[string]any) {
	if nullable, ok := s["nullable"].(bool); ok {
		delete(s, "nullable")

		// OpenAPI 3.0 types are strings, never lists.
		if t, ok := s["type"].(string); ok && nullable {
			s["type"] = []any{t, "null"}
		}
	}

	for _, b := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		excl, ok := s[b[0]].(bool)
		if !ok {
			continue
		}

		delete(s, b[0])

		if bound, ok := s[b[1]]; ok && excl {
			s[b[0]] = bound
			delete(s, b[1])
		}
	}
}
