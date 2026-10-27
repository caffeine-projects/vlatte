package mockgrpc

import (
	json "encoding/json/v2"
	"maps"
	"slices"
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
	request   describedRequest
	json      *string
	times     int64
	delay     time.Duration
	delayFunc bool      // the delay is DelayFunc's, which no mock file can hold
	scenario  [3]string // name, required state, new state
}

// Describe implements descriptor.SelfDescriptive: the mock as a mock file writes it, which the admin API returns and
// saves. A mock read from a document is described by it, with its ID. A mock built in Go is described as far as mock
// files can hold it. A Func or Echo reply can't be, even in a Seq or Rand: encoding the description of its mock fails.
// A Shared reply, a Seq or Rand reply of other replies than Response, a CallMatches matcher, a callback, a custom
// matcher or a DelayFunc delay can't be either, and is left out. A mock with a matcher or a delay left out is
// described without its reply too, so its description doesn't load back as a mock that matches more calls, or replies
// sooner.
func (m *Mock) Describe() descriptor.Descriptor {
	if m.doc != nil {
		doc := maps.Clone(m.doc)
		doc["id"] = m.id

		// The admin API enables and disables a mock without changing its document.
		if m.Enabled() {
			delete(doc, "enabled")
		} else {
			doc["enabled"] = false
		}

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

	req, complete := d.describeRequest(m.method)
	doc["request"] = req

	// A Func or Echo reply, unlike a part left out, is never in a mock file: the description fails rather than show the
	// mock.
	if err := notDescribed(m.reply); err != nil {
		doc["response"] = descriptor.Fail("response", err)
		return descriptor.Root(doc)
	}

	if !complete || d.delayFunc {
		return descriptor.Root(doc)
	}

	switch r := m.reply.(type) {
	case *Response:
		if res, ok := r.describe(); ok {
			doc["response"] = res
		}
	case *SequentialReply:
		if res, ok := r.describe(); ok {
			doc["response_sequence"] = res
		}
	case *RandomReply:
		if res, ok := r.describe(); ok {
			doc["response_random"] = res
		}
	case *BidiReply:
		if res, ok := r.describe(); ok {
			doc["bidi"] = res
		}
	case *ProxyReply:
		if res, ok := r.describe(); ok {
			doc["proxy"] = res
		}
	}

	return descriptor.Root(doc)
}

// notDescribed returns why a mock file can never hold reply, or a reply of its Seq or Rand: it is a Func or an Echo.
func notDescribed(reply Reply) error {
	var replies []Reply

	switch r := reply.(type) {
	case ReplierFunc:
		return errFuncNotDescribed
	case *EchoReply:
		return errEchoNotDescribed
	case *SequentialReply:
		replies = append(slices.Clone(r.replies), r.replyAfterSequenceEnded)
	case *RandomReply:
		replies = r.replies
	}

	for _, r := range replies {
		if err := notDescribed(r); err != nil {
			return err
		}
	}

	return nil
}

// describeRequest describes the request part of a mock of method, and reports false when a matcher is left out.
func (d *mockDescription) describeRequest(method string) (map[string]any, bool) {
	req := map[string]any{"method": method}
	complete := !d.request.undescribable

	header, ok := describeMatchers(d.request.header)
	complete = complete && ok

	if len(header) > 0 {
		req["header"] = header
	}

	fields, ok := describeMatchers(d.request.fields)
	complete = complete && ok

	if len(fields) > 0 {
		req["fields"] = fields
	}

	if d.request.body != nil {
		v, ok := describeMatcher(d.request.body)
		complete = complete && ok

		if ok {
			req["body"] = v
		}
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
		v, ok := describeMessage(d.request.message)
		complete = complete && ok

		if ok {
			req["message"] = v
		}
	case d.request.messages != nil:
		v, ok := describeMessages(d.request.messages)
		complete = complete && ok

		if ok {
			req["messages"] = v
		}
	}

	return req, complete
}

// describeMatchers describes matchers by name, leaving out those that can't be described, and reports false when it
// leaves one out.
func describeMatchers(matchers []namedMatcher) (map[string]any, bool) {
	out := make(map[string]any, len(matchers))
	complete := true

	for _, nm := range matchers {
		v, ok := describeMatcher(nm.matcher)
		if !ok {
			complete = false
			continue
		}

		out[nm.name] = v
	}

	return out, complete
}

// describeMatcher returns the matcher list of m, as matchbuild reads it.
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

		if e.AfterResponse {
			ev["after_response"] = true
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

			// Build read each text as a message of the method: it is JSON.
			var v any
			_ = json.Unmarshal([]byte(text), &v)

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

		// The details of a mock file, in detailJSON, are left out: its document describes it.
		details := make([]any, 0, len(r.details))
		for _, d := range r.details {
			v, ok := describeAny(d)
			if !ok {
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

// describe describes the SequentialReply as a mock file's response_sequence, and reports false when one can't hold
// it: a reply of the sequence that isn't a Response mock files can hold.
func (r *SequentialReply) describe() (map[string]any, bool) {
	responses, ok := describeResponses(r.replies)
	if !ok {
		return nil, false
	}

	res := map[string]any{"responses": responses}

	if r.replyAfterSequenceEnded != nil {
		after, ok := describeResponses([]Reply{r.replyAfterSequenceEnded})
		if !ok {
			return nil, false
		}

		res["after_ended"] = after[0]
	}

	return res, true
}

// describe describes the RandomReply as a mock file's response_random, and reports false when one can't hold it, as
// describeResponses does. RandWith's generator is left out: a mock file can't hold it.
func (r *RandomReply) describe() (map[string]any, bool) {
	responses, ok := describeResponses(r.replies)
	if !ok {
		return nil, false
	}

	res := map[string]any{"responses": responses}
	if r.seeded {
		res["seed"] = r.seed
	}

	return res, true
}

// describeResponses describes replies, each a Response, as a mock file's list of responses, and reports false when
// one isn't a Response, or can't be described.
func describeResponses(replies []Reply) ([]any, bool) {
	out := make([]any, 0, len(replies))
	for _, reply := range replies {
		r, ok := reply.(*Response)
		if !ok {
			return nil, false
		}

		v, ok := r.describe()
		if !ok {
			return nil, false
		}

		out = append(out, v)
	}

	return out, true
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

// describe describes the ProxyReply as a mock file writes it, and reports false when it has dial options, which no mock
// file can hold.
func (r *ProxyReply) describe() (map[string]any, bool) {
	if len(r.dialOptions) > 0 {
		return nil, false
	}

	res := map[string]any{"target": r.target}

	if h := describeMD(r.header); h != nil {
		res["header"] = h
	}

	if len(r.remove) > 0 {
		res["remove_headers"] = anys(r.remove)
	}

	return res, true
}

// describeAny describes a status detail as an Any, with its @type, as mock files write it.
func describeAny(d proto.Message) (any, bool) {
	b, err := protojson.Marshal(d)
	if err != nil {
		return nil, false
	}

	// protojson writes a message as a JSON object.
	var v map[string]any
	_ = json.Unmarshal(b, &v)

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
