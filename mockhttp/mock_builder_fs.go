package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/viper"

	"github.com/caffeine-projects/vlatte"

	"github.com/caffeine-projects/vlatte/internal/filterbuilder"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/bufpool"
	"github.com/caffeine-projects/vlatte/mockhttp/internal/viewdata"
)

var (
	_ vlatte.Builder[*Mock, *Server] = (*mockBuilderFromFile)(nil)
	_ vlatte.Builder[*Mock, *Server] = (*mockBuilderFromBytes)(nil)
	_ error                          = (*jsonSchemaValidationError)(nil)
)

const (
	_fName             = "name"
	_fEnabled          = "enabled"
	_fPriority         = "priority"
	_fDelay            = "delay"
	_fRepeat           = "repeat"
	_fExtenders        = "ext"
	_fOnComplete       = "on_complete"
	_fOnCompleteName   = "name"
	_fOnCompleteParams = "parameters"

	_fScenario              = "scenario"
	_fScenarioName          = "scenario.name"
	_fScenarioRequiredState = "scenario.required_state"
	_fScenarioNewState      = "scenario.new_state"

	_fRequestScheme   = "request.scheme"
	_fRequestMethod   = "request.method"
	_fRequestURL      = "request.url"
	_fRequestURLMatch = "request.url_match"
	_fRequestURLPath  = "request.path"
	_fRequestQuery    = "request.query"
	_fRequestQueries  = "request.queries"
	_fRequestHeader   = "request.header"
	_fRequestForm     = "request.form_url_encoded"
	_fRequestBody     = "request.body"

	_fResponse       = "response"
	_fResponseStatus = "response.status"
	_fResponseHeader = "response.header"

	_fResponseBody     = "response.body"
	_fResponseEncoding = "response.encoding"
	_fResponseBodyFile = "response.body_file"

	_fResponseSequence        = "response_sequence"
	_fResponseSequenceEntries = "response_sequence.responses"
	_fResponseSequenceEnded   = "response_sequence.after_ended"

	_fResponseRandom        = "response_random"
	_fResponseRandomEntries = "response_random.responses"
	_fResponseRandomSeed    = "response_random.seed"

	_fResponseProxy                   = "proxy"
	_fResponseProxyTarget             = "proxy.target"
	_fResponseProxyAdditionalHeader   = "proxy.forward_header"
	_fResponseProxyHeader             = "proxy.header"
	_fResponseProxyHeaderKeysToRemove = "proxy.remove_headers"
	_fResponseProxyTrimPrefix         = "proxy.trim_prefix"
	_fResponseProxyTrimSuffix         = "proxy.trim_suffix"
	_fResponseProxyTimeout            = "proxy.timeout"
	_fResponseProxySSLVerify          = "proxy.ssl_verify"
	_fResponseProxyNoFollow           = "proxy.no_follow"
)

// Response fields
const (
	_resStatus          = "status"
	_resHeader          = "header"
	_resHeaderTemplate  = "header_template"
	_resTrailer         = "trailer"
	_resBody            = "body"
	_resBodyFile        = "body_file"
	_resTemplateEnabled = "template.enabled"
	_resTemplateData    = "template.data"
	_resEncoding        = "encoding"
)

var _mockJSONSchema = mockSchemaJSON

// _mockJSONSchemaURL identifies the embedded schema. It is not a file path, so error messages
// don't depend on the working directory.
const _mockJSONSchemaURL = "mem://vlatte/mock.schema.json"

var _mockSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(_mockJSONSchema))
	if err != nil {
		return nil, err
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)

	if err = c.AddResource(_mockJSONSchemaURL, doc); err != nil {
		return nil, err
	}

	return c.Compile(_mockJSONSchemaURL)
})

type jsonSchemaValidationError struct {
	err *jsonschema.ValidationError
}

func (e jsonSchemaValidationError) Error() string {
	return "mock definition did not pass the schema validation:\n" + e.err.Error()
}

func validateMockSchema(doc any) error {
	schema, err := _mockSchema()
	if err != nil {
		return fmt.Errorf("failed to compile the mock definition schema: %w", err)
	}

	if err = schema.Validate(doc); err != nil {
		if ve, ok := errors.AsType[*jsonschema.ValidationError](err); ok {
			return &jsonSchemaValidationError{ve}
		}

		return fmt.Errorf("mock definition did not pass the schema validation: %w", err)
	}

	return nil
}

type mockBuilderFromFile struct {
	builder  *Builder
	filename string
}

// FromFile builds a Mock from the given filename.
// It accepts the same extensions from viper.Viper.
// Every mock configuration file is treated as a Go template.
// Check the documentation to see the available data to be used within the template.
// Since every mock file is a Go template by default,
// if you need to define templates for the response URL, header or body, remember to escape it.
// Eg.: body: {{`{{ .Request.Method }}`}}
func FromFile(filename string) vlatte.Builder[*Mock, *Server] {
	return &mockBuilderFromFile{filename: filename, builder: Request()}
}

func (b *mockBuilderFromFile) Build(app *Server) (mock *Mock, err error) {
	mock, err = b.build(app)
	if err != nil {
		return nil, fmt.Errorf("mock: failed to build mock from file %s.\n%w", b.filename, err)
	}

	return mock, nil
}

func (b *mockBuilderFromFile) build(app *Server) (mock *Mock, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to build mock from file %s.\n%v", b.filename, r)
		}
	}()

	_, err = os.Stat(b.filename)

	if os.IsNotExist(err) {
		return nil, fmt.Errorf("file %s does not exist", b.filename)
	}

	content, err := os.ReadFile(b.filename)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(b.filename, ".")
	ext := parts[len(parts)-1]

	b.builder.SetSource(b.filename)

	return BuildMockFromBytes(app, b.builder, content, ext, true)
}

type mockBuilderFromBytes struct {
	builder *Builder
	bytes   []byte
	ext     string
}

func FromBytes(b []byte, extension string) vlatte.Builder[*Mock, *Server] {
	return &mockBuilderFromBytes{bytes: b, builder: Request(), ext: extension}
}

func (b *mockBuilderFromBytes) Build(app *Server) (mock *Mock, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mock: error building mock from byte array. panic: %v", r)
		}
	}()

	mock, err = BuildMockFromBytes(app, b.builder, b.bytes, b.ext, true)
	if err != nil {
		return nil, fmt.Errorf("mock: error building mock from byte array.\n%w", err)
	}

	return mock, nil
}

// buildAdminMock builds a mock definition received by the admin API.
// Like the builders above, it turns panics caused by malformed definitions into errors.
func buildAdminMock(app *Server, content []byte, ext string) (mock *Mock, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mock: error building mock from byte array. panic: %v", r)
		}
	}()

	return BuildMockFromBytes(app, Request(), content, ext, false)
}

// buildReply expects a sub instance of viper.Viper, containing the response definition.
// Fields should not be accessed using "response.", given that the argument v already contains only the
// response.* fields.
func buildReply(vi *viper.Viper) (vlatte.Replier[ReplyInput, *Response], error) {
	vi.SetDefault(_resStatus, http.StatusOK)
	vi.SetDefault(_resTemplateEnabled, false)

	res := NewReply()
	res.Status(vi.GetInt(_resStatus))

	for k, v := range vi.GetStringMap(_resHeader) {
		switch vv := v.(type) {
		case string:
			res.Header(k, vv)
		case []any:
			for _, h := range vv {
				s, ok := h.(string)
				if !ok {
					return nil, fmt.Errorf("[%s.%s] expected a string. got: %T", _resHeader, k, h)
				}

				res.Header(k, s)
			}
		}
	}

	for k, v := range vi.GetStringMap(_resHeaderTemplate) {
		switch vv := v.(type) {
		case string:
			res.HeaderTemplate(k, vv)
		case []any:
			for _, h := range vv {
				s, ok := h.(string)
				if !ok {
					return nil, fmt.Errorf("[%s.%s] expected a string. got: %T", _resHeaderTemplate, k, h)
				}

				res.HeaderTemplate(k, s)
			}
		}
	}

	for k, v := range vi.GetStringMap(_resTrailer) {
		switch vv := v.(type) {
		case string:
			res.Trailer(k, vv)
		case []any:
			for _, h := range vv {
				s, ok := h.(string)
				if !ok {
					return nil, fmt.Errorf("[%s.%s] expected a string. got: %T", _resTrailer, k, h)
				}

				res.Trailer(k, s)
			}
		}
	}

	switch vi.GetString(_resEncoding) {
	case "gzip":
		res.Gzip()
	}

	teData := vi.Get(_resTemplateData)
	isTemplateEnabled := vi.GetBool(_resTemplateEnabled) || teData != nil

	if teData != nil {
		res.SetTemplateData(teData)
	}

	if vi.IsSet(_resBodyFile) {
		filename := vi.GetString(_resBodyFile)

		if isTemplateEnabled {
			res.BodyFileWithTemplate(filename)
		} else {
			res.BodyFile(filename)
		}
	} else {
		body := vi.Get(_resBody)
		switch e := body.(type) {
		case string:
			if isTemplateEnabled {
				res.BodyTemplate(e)
			} else {
				res.Body([]byte(e))
			}
		case []map[string]any, map[string]any, bool, float64:
			res.BodyJSON(e)
		}
	}

	return res, nil
}

func BuildMockFromBytes(app *Server, builder *Builder, content []byte, ext string, useTemplate bool) (*Mock, error) {
	var buf *bytes.Buffer
	if useTemplate {
		tmpl, err := template.New("").Parse(string(content))
		if err != nil {
			return nil, err
		}

		// Mock files load outside any request, so parameters are read without one.
		d := &mockFileData{viewdata.NewParameters(context.Background(), app.params), app.Data(), app.URL()}
		buf = bufpool.Get()
		buf.Reset()

		defer buf.Reset()

		err = tmpl.Execute(buf, d)
		if err != nil {
			return nil, err
		}
	} else {
		buf = bytes.NewBuffer(content)
	}

	validated := false
	if ext == "json" {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(buf.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("mock definition did not pass the schema validation: %w", err)
		}

		if err = validateMockSchema(doc); err != nil {
			return nil, err
		}

		validated = true
	}

	vi := viper.New()
	vi.SetConfigType(ext)

	// Setting defaults before reading the configuration file
	vi.SetDefault(_fEnabled, true)

	err := vi.ReadConfig(buf)
	if err != nil {
		return nil, err
	}

	if !validated {
		// Round-trip through JSON so the Go values decoded by viper become plain JSON values.
		b, err := json.Marshal(vi.AllSettings())
		if err != nil {
			return nil, fmt.Errorf("mock definition did not pass the schema validation: %w", err)
		}

		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("mock definition did not pass the schema validation: %w", err)
		}

		if err = validateMockSchema(doc); err != nil {
			return nil, err
		}
	}

	err = validateResponseDefinitions(vi)
	if err != nil {
		return nil, err
	}

	// Init building the mock specification
	// --

	// Begin General
	// --

	builder.Name(vi.GetString(_fName))
	builder.Priority(vi.GetInt(_fPriority))
	builder.Enabled(vi.GetBool(_fEnabled))

	if vi.IsSet(_fRepeat) {
		builder.Times(vi.GetInt64(_fRepeat))
	}

	if vi.IsSet(_fScenario) {
		builder.ScenarioIs(vi.GetString(_fScenarioName))
		builder.ScenarioStateIs(vi.GetString(_fScenarioRequiredState))
		builder.ScenarioStateWillBe(vi.GetString(_fScenarioNewState))
	}

	if vi.IsSet(_fDelay) {
		var duration time.Duration
		delay := vi.Get(_fDelay)

		switch v := delay.(type) {
		case string:
			duration, err = time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("[delay] error parsing delay. %w", err)
			}
		case float64:
			duration = time.Duration(v * float64(time.Millisecond))
		case int:
			duration = time.Duration(float64(v) * float64(time.Millisecond))
		default:
			return nil, fmt.Errorf("[delay] type %T is not supported. supported types: string, number", v)
		}

		builder.Delay(duration)
	}

	if vi.IsSet(_fOnComplete) {
		raw := vi.Get(_fOnComplete)
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("[on_complete] expected an array of objects. got: %T", raw)
		}

		for i, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("[on_complete][%d] expected an object. got: %T", i, item)
			}

			name, ok := obj[_fOnCompleteName].(string)
			if !ok {
				return nil, fmt.Errorf("[on_complete][%d].%s expected a string. got: %T", i, _fOnCompleteName, obj[_fOnCompleteName])
			}

			builder.OnCompleteEvent(vlatte.EventConfig{
				Name:       name,
				Parameters: obj[_fOnCompleteParams],
			})
		}
	}

	// --
	// End General

	// Begin Request
	// --

	if vi.IsSet(_fRequestMethod) {
		mv := vi.Get(_fRequestMethod)

		switch t := mv.(type) {
		case string:
			methods := strings.Split(t, ",")
			for i, v := range methods {
				methods[i] = strings.TrimSpace(v)
			}

			builder.Method(methods...)
		default:
			m, err := filterbuilder.BuildMatcher(mv)
			if err != nil {
				return nil, fmt.Errorf("[request.method] error building matcher %v.\n%w", mv, err)
			}

			builder.MethodMatches(m)
		}
	}

	if vi.IsSet(_fRequestScheme) {
		scheme := vi.Get(_fRequestScheme)

		switch t := scheme.(type) {
		case string:
			builder.Scheme(t)
		default:
			m, err := filterbuilder.BuildMatcher(scheme)
			if err != nil {
				return nil, fmt.Errorf("[request.scheme] error building matcher %v.\n%w", scheme, err)
			}

			builder.SchemeMatches(m)
		}
	}

	if vi.IsSet(_fRequestURL) {
		uv := vi.Get(_fRequestURL)

		switch e := uv.(type) {
		case string:
			u, err := url.Parse(e)
			if err != nil {
				return nil, fmt.Errorf("[request.url] error parsing url \"%s\".\n%w", e, err)
			}

			if u.IsAbs() {
				builder.URL(match.EqualIgnoreCase(e))
			} else {
				builder.URL(match.URLPath(u.Path))
			}
		default:
			m, err := filterbuilder.BuildMatcher(uv)
			if err != nil {
				return nil, fmt.Errorf("[request.url] error building url.\n%w", err)
			}

			builder.URL(m)
		}
	} else if vi.IsSet(_fRequestURLMatch) {
		builder.URL(match.RegExpr(vi.GetString(_fRequestURLMatch)))
	} else if vi.IsSet(_fRequestURLPath) {
		uv := vi.Get(_fRequestURLPath)

		switch e := uv.(type) {
		case string:
			builder.URL(match.URLPath(e))
		default:
			m, err := filterbuilder.BuildMatcher(uv)
			if err != nil {
				return nil, fmt.Errorf("[request.url_path] error building matcher.\n%w", err)
			}

			builder.URLPath(m)
		}
	}

	for k, v := range vi.GetStringMap(_fRequestQuery) {
		m, err := filterbuilder.TryBuildMatcher(v)
		if err != nil {
			return nil,
				fmt.Errorf("[request.query[%s]] error building matcher.\n%w", k, err)
		}

		builder.Query(k, m)
	}

	for k, v := range vi.GetStringMap(_fRequestQueries) {
		m, err := filterbuilder.TryBuildMatcher(v)
		if err != nil {
			return nil,
				fmt.Errorf("[request.queries[%s]] error building matcher.\n%w", k, err)
		}

		builder.Queries(k, m)
	}

	for k, v := range vi.GetStringMap(_fRequestHeader) {
		m, err := filterbuilder.TryBuildMatcher(v)
		if err != nil {
			return nil,
				fmt.Errorf("[request.header[%s]] error building matcher.\n%w", k, err)
		}

		builder.Header(k, m)
	}

	for k, v := range vi.GetStringMap(_fRequestForm) {
		m, err := filterbuilder.TryBuildMatcher(v)
		if err != nil {
			return nil,
				fmt.Errorf("[request.form[%s]] error building matcher.\n%w", k, err)
		}

		builder.FormField(k, m)
	}

	if vi.IsSet(_fRequestBody) {
		m, err := filterbuilder.TryBuildMatcher(vi.Get(_fRequestBody))
		if err != nil {
			return nil,
				fmt.Errorf("[request.body] error building matcher.\n%w", err)
		}

		builder.Body(m)
	}

	// --
	// End Request

	// Begin Response
	// --

	var rep vlatte.Replier[ReplyInput, *Response]

	if vi.IsSet(_fResponse) {
		rep, err = buildReply(vi.Sub(_fResponse))
		if err != nil {
			return nil, err
		}
	} else if vi.IsSet(_fResponseRandom) {
		if !vi.IsSet(_fResponseRandomEntries) {
			return nil, errors.New("[response_random.responses] requires at least one response definition")
		}

		var random *RandomReply

		if vi.IsSet(_fResponseRandomSeed) {
			random = RandWithSeed(vi.GetInt64(_fResponseRandomSeed))
		} else {
			random = Rand()
		}

		entries, ok := vi.Get(_fResponseRandomEntries).([]any)
		if !ok {
			return nil, errors.New("[response_random.response] requires an array of responses")
		}

		for i, r := range entries {
			m, ok := r.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("[%s[%d]] expected an object. got: %T", _fResponseRandomEntries, i, r)
			}

			sub := viper.New()
			err = sub.MergeConfigMap(m)
			if err != nil {
				return nil,
					fmt.Errorf("[response_random.responses[%d]] error building random response.\n%w", i, err)
			}

			rr, err := buildReply(sub)
			if err != nil {
				return nil,
					fmt.Errorf("[response_random.responses[%d]] building error.\n%w", i, err)
			}

			random.Add(rr)
		}

		rep = random
	} else if vi.IsSet(_fResponseSequence) {
		if !vi.IsSet(_fResponseSequenceEntries) {
			return nil, errors.New("[response_sequence] requires at least one response definition")
		}

		seq := Seq()

		if vi.IsSet(_fResponseSequenceEnded) {
			rr, err := buildReply(vi.Sub(_fResponseSequenceEnded))
			if err != nil {
				return nil,
					fmt.Errorf("[response_response.sequence_ended] building error.\n%w", err)
			}

			seq.OnSequenceEnded(rr)
		}

		entries, ok := vi.Get(_fResponseSequenceEntries).([]any)
		if !ok {
			return nil, errors.New("[response_sequence.response] requires an array of responses")
		}

		for i, r := range entries {
			m, ok := r.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("[%s[%d]] expected an object. got: %T", _fResponseSequenceEntries, i, r)
			}

			sub := viper.New()
			err = sub.MergeConfigMap(m)
			if err != nil {
				return nil, err
			}

			rr, err := buildReply(sub)
			if err != nil {
				return nil,
					fmt.Errorf("[response_sequence.responses[%d]] building error.\n%w", i, err)
			}

			seq.Add(rr)
		}

		rep = seq
	} else if vi.IsSet(_fResponseProxy) {
		if !vi.IsSet(_fResponseProxyTarget) {
			return nil, errors.New("[proxy.target] is required")
		}

		target := vi.GetString(_fResponseProxyTarget)
		proxy := From(target)

		for k, v := range vi.GetStringMapString(_fResponseProxyAdditionalHeader) {
			proxy.ForwardHeader(k, v)
		}

		for k, v := range vi.GetStringMapString(_fResponseProxyHeader) {
			proxy.Header(k, v)
		}

		for _, k := range vi.GetStringSlice(_fResponseProxyHeaderKeysToRemove) {
			proxy.RemoveProxyHeaders(k)
		}

		if vi.IsSet(_fResponseProxyTrimPrefix) {
			proxy.TrimPrefix(vi.GetString(_fResponseProxyTrimPrefix))
		}

		if vi.IsSet(_fResponseProxyTrimSuffix) {
			proxy.TrimSuffix(vi.GetString(_fResponseProxyTrimSuffix))
		}

		if vi.IsSet(_fResponseProxyTimeout) {
			proxy.Timeout(vi.GetDuration(_fResponseProxyTimeout))
		}

		if vi.IsSet(_fResponseProxySSLVerify) && !vi.GetBool(_fResponseProxySSLVerify) {
			proxy.SkipSSLVerify()
		}

		if vi.IsSet(_fResponseProxyNoFollow) && vi.GetBool(_fResponseProxyNoFollow) {
			proxy.NoFollow()
		}

		rep = proxy
	} else {
		// no response definition found.
		// default to 200 (OK) with nothing more.
		rep = OK()
	}

	builder.Reply(rep)

	// --
	// End Response

	// User defined handlers

	if len(app.config.MockFileHandlers) > 0 && vi.IsSet(_fExtenders) {
		settings := vi.GetStringMap(_fExtenders)

		for i, handler := range app.config.MockFileHandlers {
			err = handler.Handle(settings, builder)
			if err != nil {
				return nil, fmt.Errorf("custom_field_handler: field handler at index %d failed.\n%w", i, err)
			}
		}
	}

	return builder.Build(app)
}

func validateResponseDefinitions(vi *viper.Viper) error {
	val := 0
	if vi.IsSet(_fResponse) {
		val++
	}
	if vi.IsSet(_fResponseProxy) {
		val++
	}
	if vi.IsSet(_fResponseSequence) {
		val++
	}
	if vi.IsSet(_fResponseRandom) {
		val++
	}

	if val > 1 {
		return errors.New("more than one response definition set")
	}

	return nil
}
