package mockgrpc

import (
	json "encoding/json/v2"
	"maps"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
)

var _ descriptor.SelfDescriptive = (*Mock)(nil)

// describedRequest is the request part of a builder, as a mock file writes it.
type describedRequest struct {
	header        []namedMatcher
	fields        []namedMatcher
	body          vlatte.Matcher
	message       proto.Message
	messages      []proto.Message
	undescribable bool // a matcher no mock file can hold, such as CallMatches's
}

type namedMatcher struct {
	name    string
	matcher vlatte.Matcher
}

// mockDescription is what a mock built in Go keeps of its builder, to describe itself.
type mockDescription struct {
	request  describedRequest
	json     *string
	times    int64
	delay    time.Duration
	scenario [3]string // name, required state, new state
}

// Describe implements descriptor.SelfDescriptive: the mock as a mock file writes it, which the admin API returns and
// saves. A mock read from a document is described by it, with its ID. A mock built in Go is described as far as mock
// files can hold it: a Func reply, a CallMatches matcher, a callback, a custom matcher or a DelayFunc delay can't be,
// and is left out, so a description without a response doesn't load back.
func (m *Mock) Describe() descriptor.Descriptor {
	if m.doc != nil {
		doc := maps.Clone(m.doc)
		doc["id"] = m.id

		return descriptor.Root(doc)
	}

	doc := map[string]any{"id": m.id}
	d := m.described

	if m.name != "" {
		doc["name"] = m.name
	}

	if m.priority != 0 {
		doc["priority"] = m.priority
	}

	if !m.Enabled() {
		doc["enabled"] = false
	}

	if d.delay > 0 {
		doc["delay"] = d.delay.String()
	}

	if d.times > 0 {
		doc["repeat"] = d.times
	}

	if d.scenario[0] != "" {
		sc := map[string]any{"name": d.scenario[0]}
		if d.scenario[1] != "" {
			sc["required_state"] = d.scenario[1]
		}

		if d.scenario[2] != "" {
			sc["new_state"] = d.scenario[2]
		}

		doc["scenario"] = sc
	}

	if events := describeEvents(m.events); len(events) > 0 {
		doc["on_complete"] = events
	}

	doc["request"] = d.describeRequest(m.method)

	switch r := m.reply.(type) {
	case *Response:
		if res, ok := r.describe(); ok {
			doc["response"] = res
		}
	case *BidiReply:
		if res, ok := r.describe(); ok {
			doc["bidi"] = res
		}
	}

	return descriptor.Root(doc)
}

// describeRequest describes the request part of a mock of method.
func (d *mockDescription) describeRequest(method string) map[string]any {
	req := map[string]any{"method": method}

	if header := describeMatchers(d.request.header); len(header) > 0 {
		req["header"] = header
	}

	if fields := describeMatchers(d.request.fields); len(fields) > 0 {
		req["fields"] = fields
	}

	if v, ok := describeMatcher(d.request.body); ok {
		req["body"] = v
	}

	switch {
	case d.json != nil:
		var v any
		if err := json.Unmarshal([]byte(*d.json), &v); err == nil {
			if _, isList := v.([]any); isList {
				req["messages"] = v
			} else {
				req["message"] = v
			}
		}
	case d.request.message != nil:
		if v, ok := describeMessage(d.request.message); ok {
			req["message"] = v
		}
	case d.request.messages != nil:
		if v, ok := describeMessages(d.request.messages); ok {
			req["messages"] = v
		}
	}

	return req
}

// describeMatchers describes matchers by name, leaving out those that can't be described.
func describeMatchers(matchers []namedMatcher) map[string]any {
	out := make(map[string]any, len(matchers))
	for _, nm := range matchers {
		if v, ok := describeMatcher(nm.matcher); ok {
			out[nm.name] = v
		}
	}

	return out
}

// describeMatcher returns the matcher list of m, as filterbuilder reads it.
func describeMatcher(m vlatte.Matcher) (any, bool) {
	if m == nil {
		return nil, false
	}

	d, ok := descriptor.IsSelfDescriptive(m)
	if !ok {
		return nil, false
	}

	v, err := d.Marshallable()

	return v, err == nil
}

// describeEvents describes the events that name a listener: one that carries its own, as callbacks do, can't be.
func describeEvents(events vlatte.EventConfigs) []any {
	var out []any

	for _, e := range events {
		if e.Listener != nil {
			continue
		}

		ev := map[string]any{"name": e.Name}
		if e.Parameters != nil {
			ev["parameters"] = e.Parameters
		}

		out = append(out, ev)
	}

	return out
}

// describeMessage returns msg as protojson writes it, as a JSON value.
func describeMessage(msg proto.Message) (any, bool) {
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return nil, false
	}

	var v any

	return v, json.Unmarshal(b, &v) == nil
}

// describeMessages describes each of msgs, as describeMessage does.
func describeMessages(msgs []proto.Message) ([]any, bool) {
	out := make([]any, 0, len(msgs))
	for _, msg := range msgs {
		v, ok := describeMessage(msg)
		if !ok {
			return nil, false
		}

		out = append(out, v)
	}

	return out, true
}

// describe describes the Response as a mock file's response, and reports false when one can't hold it, as for a
// DelayFunc delay.
func (r *Response) describe() (map[string]any, bool) {
	res := make(map[string]any)

	switch {
	case r.json != nil:
		msgs := make([]any, 0, len(r.json))
		for _, text := range r.json {
			if r.templated {
				msgs = append(msgs, text)
				continue
			}

			var v any
			if err := json.Unmarshal([]byte(text), &v); err != nil {
				return nil, false
			}

			msgs = append(msgs, v)
		}

		res["messages"] = msgs
		if r.templated {
			res["template"] = true
		}
	case r.messages != nil:
		msgs, ok := describeMessages(r.messages)
		if !ok {
			return nil, false
		}

		res["messages"] = msgs
	}

	if h := describeMD(r.header); h != nil {
		res["header"] = h
	}

	if t := describeMD(r.trailer); t != nil {
		res["trailer"] = t
	}

	if r.code != 0 || r.message != "" {
		st := map[string]any{"code": codeName(r.code), "message": r.message}

		details := make([]any, 0, len(r.details)+len(r.detailJSON))
		for _, d := range r.details {
			v, ok := describeAny(d)
			if !ok {
				return nil, false
			}

			details = append(details, v)
		}

		for _, text := range r.detailJSON {
			var v any
			if err := json.Unmarshal([]byte(text), &v); err != nil {
				return nil, false
			}

			details = append(details, v)
		}

		if len(details) > 0 {
			st["details"] = details
		}

		res["status"] = st
	}

	if r.delay != nil {
		if r.delayDuration == 0 {
			return nil, false
		}

		res["message_delay"] = r.delayDuration.String()
	}

	return res, true
}

// describe describes the BidiReply as a mock file's bidi, and reports false when one can't hold it.
func (b *BidiReply) describe() (map[string]any, bool) {
	res := make(map[string]any)

	if h := describeMD(b.header); h != nil {
		res["header"] = h
	}

	if t := describeMD(b.trailer); t != nil {
		res["trailer"] = t
	}

	rules := make([]any, 0, len(b.rules))
	for _, rule := range b.rules {
		when, ok := describeMatcher(rule.matcher)
		if !ok {
			return nil, false
		}

		response, ok := rule.response.describe()
		if !ok {
			return nil, false
		}

		rules = append(rules, map[string]any{"when": when, "response": response})
	}

	if len(rules) > 0 {
		res["on"] = rules
	}

	for key, r := range map[string]*Response{"otherwise": b.otherwise, "on_end": b.end} {
		if r == nil {
			continue
		}

		v, ok := r.describe()
		if !ok {
			return nil, false
		}

		res[key] = v
	}

	return res, true
}

// describeAny describes a status detail as an Any, with its @type, as mock files write it.
func describeAny(d proto.Message) (any, bool) {
	b, err := protojson.Marshal(d)
	if err != nil {
		return nil, false
	}

	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, false
	}

	if _, isAny := v["@type"]; !isAny {
		v["@type"] = "type.googleapis.com/" + string(d.ProtoReflect().Descriptor().FullName())
	}

	return v, true
}

// describeMD describes metadata, a key's values as a list.
func describeMD(md metadata.MD) map[string]any {
	if len(md) == 0 {
		return nil
	}

	out := make(map[string]any, len(md))
	for k, v := range md {
		out[strings.ToLower(k)] = append([]any(nil), anys(v)...)
	}

	return out
}

func anys(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}

	return out
}
