package mockhttp

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
)

// Generators for the property tests of mock files. A model is a mock as Go values: it renders as a
// mock document, and it predicts how the served mock answers requests, which is what the tests check.

// _genMethods are the methods models use. HEAD is left out: its responses have no body to check.
var _genMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// _genStatuses are statuses whose responses carry a body and aren't redirects the client would follow.
var _genStatuses = []int{200, 201, 202, 203, 206, 400, 401, 403, 404, 409, 418, 422, 500, 503}

// genToken draws text that is safe as a header value, a query value or a path segment, and never holds '~',
// which samples add to make a value differ.
func genToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9_.-]{1,8}`)
}

// matcherModel is a matcher value of a mock file and what it matches.
type matcherModel struct {
	spec  any               // the value in the file: text, or a matcher list
	match func(string) bool // whether a request value matches; "" is a value that is absent
	// sample draws a request value that matches, or doesn't; "" means absent.
	sample func(t *rapid.T, matching bool) string
}

func genMatcher() *rapid.Generator[matcherModel] {
	return rapid.Custom(func(t *rapid.T) matcherModel {
		v := genToken().Draw(t, "value")

		// Matcher names are case-insensitive.
		name := func(n string) string {
			return rapid.SampledFrom([]string{n, strings.ToLower(n), strings.ToUpper(n)}).Draw(t, "name case")
		}

		equal := func(s string) bool { return s == v }
		equalSample := func(_ *rapid.T, matching bool) string {
			if matching {
				return v
			}

			return v + "~"
		}

		switch rapid.IntRange(0, 5).Draw(t, "matcher") {
		case 0: // Text is compared as is.
			return matcherModel{spec: v, match: equal, sample: equalSample}
		case 1:
			return matcherModel{spec: []any{name("equal"), v}, match: equal, sample: equalSample}
		case 2:
			return matcherModel{
				spec:  []any{name("eqi"), v},
				match: func(s string) bool { return strings.EqualFold(s, v) },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return flipCase(t, v)
					}

					return v + "~"
				},
			}
		case 3:
			return matcherModel{
				spec:  []any{name("hasPrefix"), v},
				match: func(s string) bool { return strings.HasPrefix(s, v) },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return v + genToken().Draw(t, "suffix")
					}

					return "~" + v
				},
			}
		case 4:
			return matcherModel{
				spec:  []any{name("contains"), v},
				match: func(s string) bool { return strings.Contains(s, v) },
				sample: func(_ *rapid.T, matching bool) string {
					if matching {
						return "~" + v + "~"
					}

					return "~"
				},
			}
		default:
			return matcherModel{
				spec:  []any{name("present")},
				match: func(s string) bool { return s != "" },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return genToken().Draw(t, "present value")
					}

					return ""
				},
			}
		}
	})
}

// flipCase changes the case of some letters of s.
func flipCase(t *rapid.T, s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if rapid.Bool().Draw(t, "flip") {
			if strings.ToUpper(string(r)) == string(r) {
				runes[i] = []rune(strings.ToLower(string(r)))[0]
			} else {
				runes[i] = []rune(strings.ToUpper(string(r)))[0]
			}
		}
	}

	return string(runes)
}

// requestModel is the request part of a mock model.
type requestModel struct {
	doc       map[string]any
	methods   []string // methods the mock accepts; all of them when hasMethod is false
	hasMethod bool
	path      string
	header    map[string]matcherModel
	query     map[string]matcherModel
	form      map[string]matcherModel
	body      *matcherModel
}

func genRequest(baseURL string) *rapid.Generator[requestModel] {
	return rapid.Custom(func(t *rapid.T) requestModel {
		r := requestModel{doc: make(map[string]any)}
		r.path = "/" + strings.Join(rapid.SliceOfN(rapid.StringMatching(`[a-z0-9-]{1,6}`), 1, 3).Draw(t, "path"), "/")

		withBody := rapid.IntRange(0, 2).Draw(t, "body kind") // 0 none, 1 form, 2 text
		if withBody == 1 {
			r.form = rapid.MapOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,6}`), genMatcher(), 1, 2).Draw(t, "form")
			r.doc["form_url_encoded"] = specs(r.form)
		}

		if withBody == 2 {
			body := genMatcher().Draw(t, "body")
			r.body = &body
			r.doc["body"] = body.spec
		}

		absolute := false

		switch rapid.IntRange(0, 5).Draw(t, "url kind") {
		case 0:
			r.doc["path"] = r.path
		case 1:
			r.doc["path"] = []any{"equal", r.path}
		case 2:
			r.doc["url"] = r.path
		case 3:
			// An absolute URL is compared with the whole request URL, query included, so it goes without a query.
			r.doc["url"] = strings.ToUpper(baseURL[:4]) + baseURL[4:] + r.path
			absolute = true
		case 4:
			r.doc["url"] = []any{"urlPath", r.path}
		default:
			r.doc["url_match"] = `^http://[^/]+` + regexp.QuoteMeta(r.path) + `(\?.*)?$`
		}

		if !absolute && rapid.Bool().Draw(t, "has query") {
			// Mixed-case names: query names are case-sensitive, so their case must survive decoding.
			r.query = rapid.MapOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,6}`), genMatcher(), 1, 2).Draw(t, "query")
			r.doc["query"] = specs(r.query)
		}

		if rapid.Bool().Draw(t, "has header") {
			// Header names ignore case, so names that differ only in case would be one header.
			names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-[A-Za-z][A-Za-z0-9]{0,5}`), 1, 2, strings.ToLower).Draw(t, "header names")

			r.header = make(map[string]matcherModel, len(names))
			for _, name := range names {
				r.header[name] = genMatcher().Draw(t, "header")
			}

			r.doc["header"] = specs(r.header)
		}

		// A request with a body is a POST, so the methods hold POST.
		methods := rapid.SliceOfNDistinct(rapid.SampledFrom(_genMethods), 1, len(_genMethods), func(m string) string { return m }).Draw(t, "methods")
		if withBody > 0 && !slices.Contains(methods, http.MethodPost) {
			methods = append(methods, http.MethodPost)
		}

		r.methods = methods

		switch form := rapid.IntRange(0, 3).Draw(t, "method form"); {
		case form == 0 && withBody == 0:
			r.methods = _genMethods // no method key: any method
		case form == 1 && len(methods) == 1:
			r.hasMethod = true
			r.doc["method"] = strings.ToLower(methods[0]) // one method is compared ignoring case
		case form == 2:
			r.hasMethod = true
			r.doc["method"] = []any{"isIn", toAny(methods)}
		default:
			r.hasMethod = true
			r.doc["method"] = strings.Join(methods, rapid.SampledFrom([]string{",", ", ", " ,"}).Draw(t, "separator"))
		}

		return r
	})
}

func specs(matchers map[string]matcherModel) map[string]any {
	out := make(map[string]any, len(matchers))
	for k, m := range matchers {
		out[k] = m.spec
	}

	return out
}

func toAny[E any](s []E) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}

	return out
}

// requestSpec is a request to send.
type requestSpec struct {
	method string
	path   string
	query  url.Values
	header http.Header
	form   url.Values
	body   *string
	what   string // what the request changes from a matching one, for failure messages
}

// matching draws a request the mock matches.
func (r requestModel) matching(t *rapid.T) requestSpec {
	req := requestSpec{
		method: rapid.SampledFrom(r.methods).Draw(t, "method"),
		path:   r.path,
		query:  make(url.Values),
		header: make(http.Header),
		what:   "matching",
	}

	if r.body != nil || r.form != nil {
		req.method = http.MethodPost
	}

	for k, m := range r.query {
		if v := m.sample(t, true); v != "" {
			req.query.Set(k, v)
		}
	}

	for k, m := range r.header {
		if v := m.sample(t, true); v != "" {
			req.header.Set(k, v)
		}
	}

	if r.form != nil {
		req.form = make(url.Values)
		for k, m := range r.form {
			if v := m.sample(t, true); v != "" {
				req.form.Set(k, v)
			}
		}
	}

	if r.body != nil {
		body := r.body.sample(t, true)
		req.body = &body
	}

	return req
}

// mismatches draws requests that differ from a matching one in one matched part each.
func (r requestModel) mismatches(t *rapid.T) []requestSpec {
	var out []requestSpec

	if r.hasMethod && r.body == nil && r.form == nil && len(r.methods) < len(_genMethods) {
		req := r.matching(t)
		req.method = rapid.SampledFrom(slices.DeleteFunc(slices.Clone(_genMethods), func(m string) bool {
			return slices.Contains(r.methods, m)
		})).Draw(t, "other method")
		req.what = "method " + req.method
		out = append(out, req)
	}

	req := r.matching(t)
	req.path += "x"
	req.what = "path " + req.path
	out = append(out, req)

	for k, m := range r.query {
		req := r.matching(t)
		setOrDelete(req.query, k, m.sample(t, false))
		req.what = "query " + k
		out = append(out, req)

		// Query names are case-sensitive: the same value under another case doesn't match.
		if other := flipCase(t, k); other != k {
			req := r.matching(t)
			v := req.query.Get(k)
			req.query.Del(k)
			if v == "" {
				v = genToken().Draw(t, "value")
			}

			req.query.Set(other, v)
			req.what = "query name " + other
			out = append(out, req)
		}
	}

	for k, m := range r.header {
		req := r.matching(t)
		if v := m.sample(t, false); v == "" {
			req.header.Del(k)
		} else {
			req.header.Set(k, v)
		}

		req.what = "header " + k
		out = append(out, req)
	}

	for k, m := range r.form {
		req := r.matching(t)
		setOrDelete(req.form, k, m.sample(t, false))
		req.what = "form " + k
		out = append(out, req)
	}

	if r.body != nil {
		req := r.matching(t)
		body := r.body.sample(t, false)
		req.body = &body
		req.what = "body"
		out = append(out, req)
	}

	return out
}

func setOrDelete(values url.Values, k, v string) {
	if v == "" {
		values.Del(k)
		return
	}

	values.Set(k, v)
}

// replyModel is a reply of a mock model and the response it predicts.
type replyModel struct {
	doc      map[string]any
	status   int
	header   []headerValue
	template []string // headers that render the request method
	trailer  []headerValue
	text     *string
	json     any // a body that is not text, served as JSON
	hasJSON  bool
	data     *string // a body rendering .Extra.name, set to this value
	gzip     bool
}

func genReply() *rapid.Generator[replyModel] {
	return rapid.Custom(func(t *rapid.T) replyModel {
		r := replyModel{doc: make(map[string]any)}

		r.status = rapid.SampledFrom(_genStatuses).Draw(t, "status")
		r.doc["status"] = int64(r.status)

		if names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-R[a-z0-9]{0,4}`), 0, 2, strings.ToLower).Draw(t, "headers"); len(names) > 0 {
			doc := make(map[string]any)
			for _, name := range names {
				values := rapid.SliceOfN(genToken(), 1, 2).Draw(t, "values")
				for _, v := range values {
					r.header = append(r.header, headerValue{name: name, value: v})
				}

				if len(values) == 1 && rapid.Bool().Draw(t, "one value as text") {
					doc[name] = values[0]
				} else {
					doc[name] = toAny(values)
				}
			}

			r.doc["header"] = doc
		}

		if names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-T[a-z0-9]{0,4}`), 0, 2, strings.ToLower).Draw(t, "templates"); len(names) > 0 {
			doc := make(map[string]any)
			for _, name := range names {
				doc[name] = "{{ .Request.Method }}"
			}

			r.template = names
			r.doc["header_template"] = doc
		}

		if rapid.Bool().Draw(t, "has trailer") {
			v := genToken().Draw(t, "trailer")
			r.trailer = []headerValue{{name: "X-End", value: v}}
			r.doc["trailer"] = map[string]any{"X-End": v}
		}

		switch rapid.IntRange(0, 3).Draw(t, "body") {
		case 1:
			text := rapid.String().Draw(t, "text")
			r.text = &text
			r.doc["body"] = text
		case 2:
			r.json = genJSON(2).Draw(t, "json")
			r.hasJSON = true
			r.doc["body"] = r.json
		case 3:
			v := genToken().Draw(t, "data")
			r.data = &v
			r.doc["body"] = "{{ .Extra.name }}"
			r.doc["template"] = map[string]any{"data": map[string]any{"name": v}}
		}

		if rapid.Bool().Draw(t, "gzip") {
			r.gzip = true
			r.doc["encoding"] = "gzip"
		}

		return r
	})
}

// genJSON draws a body that isn't text: an object, a list, a number or a bool.
func genJSON(depth int) *rapid.Generator[any] {
	scalars := []*rapid.Generator[any]{
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.Int64Range(-1<<53, 1<<53), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-1e6, 1e6).Filter(func(f float64) bool { return f != float64(int64(f)) }), func(f float64) any { return f }),
	}

	// Inside a list or an object, values can also be text, null or nested lists.
	values := slices.Concat(scalars, []*rapid.Generator[any]{rapid.Map(genToken(), func(s string) any { return s }), rapid.Just[any](nil)})
	if depth > 0 {
		values = slices.Concat(values, []*rapid.Generator[any]{
			rapid.Map(rapid.SliceOfN(rapid.Deferred(func() *rapid.Generator[any] { return genJSON(depth - 1) }), 0, 3), func(s []any) any { return s }),
		})
	}

	return rapid.OneOf(slices.Concat(scalars, []*rapid.Generator[any]{
		rapid.Map(rapid.SliceOfN(rapid.OneOf(values...), 0, 3), func(s []any) any { return s }),
		rapid.Map(rapid.MapOfN(genToken(), rapid.OneOf(values...), 0, 3), func(m map[string]any) any { return m }),
	})...)
}

// mockModel is a mock as Go values.
type mockModel struct {
	id       string
	enabled  bool
	delay    time.Duration
	repeat   int64
	request  requestModel
	reply    *replyModel  // response
	sequence []replyModel // response_sequence
	after    *replyModel  // response_sequence.after_ended
	random   []replyModel // response_random
	seed     int64
}

func genMock(baseURL string) *rapid.Generator[mockModel] {
	return rapid.Custom(func(t *rapid.T) mockModel {
		m := mockModel{enabled: true, request: genRequest(baseURL).Draw(t, "request")}

		if rapid.Bool().Draw(t, "has id") {
			m.id = rapid.StringMatching(`[A-Za-z0-9._~-]{1,12}`).Draw(t, "id")
		}

		if rapid.IntRange(0, 9).Draw(t, "disabled") == 0 {
			m.enabled = false
		}

		if rapid.Bool().Draw(t, "has delay") {
			m.delay = time.Duration(rapid.IntRange(0, 3).Draw(t, "delay ms")) * time.Millisecond
		}

		if rapid.Bool().Draw(t, "has repeat") {
			m.repeat = rapid.Int64Range(1, 3).Draw(t, "repeat")
		}

		switch rapid.IntRange(0, 3).Draw(t, "reply kind") {
		case 0: // no response: 200 with nothing more
		case 1:
			r := genReply().Draw(t, "reply")
			m.reply = &r
		case 2:
			m.sequence = rapid.SliceOfN(genReply(), 1, 3).Draw(t, "sequence")
			if rapid.Bool().Draw(t, "has after_ended") {
				r := genReply().Draw(t, "after_ended")
				m.after = &r
			}
		default:
			m.random = rapid.SliceOfN(genReply(), 1, 3).Draw(t, "random")
			m.seed = rapid.Int64().Draw(t, "seed")
		}

		return m
	})
}

func (m mockModel) doc() map[string]any {
	doc := map[string]any{"request": m.request.doc}

	if m.id != "" {
		doc["id"] = m.id
	}

	if !m.enabled {
		doc["enabled"] = false
	}

	if m.delay > 0 {
		doc["delay"] = m.delay.String()
	}

	if m.repeat > 0 {
		doc["repeat"] = m.repeat
	}

	replies := func(rs []replyModel) []any {
		out := make([]any, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.doc)
		}

		return out
	}

	switch {
	case m.reply != nil:
		doc["response"] = m.reply.doc
	case m.sequence != nil:
		seq := map[string]any{"responses": replies(m.sequence)}
		if m.after != nil {
			seq["after_ended"] = m.after.doc
		}

		doc["response_sequence"] = seq
	case m.random != nil:
		doc["response_random"] = map[string]any{"responses": replies(m.random), "seed": m.seed}
	}

	return doc
}

// served lists the replies of the requests the mock answers, in order, up to what the test sends.
func (m mockModel) served() []replyModel {
	var out []replyModel

	switch {
	case m.reply != nil:
		out = []replyModel{*m.reply, *m.reply}
	case m.sequence != nil:
		out = slices.Clone(m.sequence)
		if m.after != nil {
			out = append(out, *m.after, *m.after)
		}
	case m.random != nil:
		out = []replyModel{{}, {}} // checked against every reply of random
	default:
		out = []replyModel{{status: http.StatusOK}, {status: http.StatusOK}}
	}

	if m.repeat > 0 && int64(len(out)) > m.repeat {
		out = out[:m.repeat]
	}

	return out
}

// encodeMock writes a mock document in a format.
func encodeMock(t *rapid.T, doc map[string]any, format string) []byte {
	b, err := config.Encode(doc, format)
	if err != nil {
		t.Fatalf("encoding the mock as %s: %v", format, err)
	}

	return b
}
