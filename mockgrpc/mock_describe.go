package mockgrpc

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/delay"
)

var _ vlatte.SelfDescriptive = (*Mock)(nil)

var (
	errCallMatchesNotDescribed = errors.New("mock: a CallMatches matcher can't be described as a mock file")
	errMatcherNotDescribed     = errors.New("mock: a matcher that doesn't describe itself can't be described as a mock file")
	errCallbackNotDescribed    = errors.New("mock: a callback, or an event with its own listener, can't be described as a mock file")
	errSharedNotDescribed      = errors.New("reply: a Shared reply can't be described as a mock file")
	errDialOptionsNotDescribed = errors.New("reply: a From reply with dial options can't be described as a mock file")
	errReplyNotDescribed       = errors.New("reply: mock files have no such reply")
	errResponsesOnly           = errors.New("reply: a Seq or Rand of a mock file holds only Message, Messages, JSON, " +
		"Template and Status replies")
)

// describedRequest is the request part of a builder, as a mock file writes it.
type describedRequest struct {
	header        []namedMatcher
	fields        []namedMatcher
	body          vlatte.Matcher
	custom        []vlatte.Matcher
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
	scenario [4]string // name, required state, new state, key
}

// Describe implements vlatte.SelfDescriptive: the mock as a mock file writes it, which the admin API returns and
// saves. A mock read from a document is described by it, with its ID. A mock built in Go is described whole, or not
// at all: a part no mock file can hold, such as a Func, Echo or Shared reply, a CallMatches matcher, a matcher that
// doesn't describe itself, a callback or a [vlatte.DelayFunc] delay, fails encoding the description, with an error that names
// it, so the admin API never shows a mock that loads back matching more calls, answering differently or without its
// callbacks. RandWith's generator is the exception: a mock file loads it back as a Rand, which picks from the same
// replies.
func (m *Mock) Describe() vlatte.Descriptor {
	if m.doc != nil {
		doc := maps.Clone(m.doc)
		doc["id"] = m.id

		// The admin API enables and disables a mock without changing its document.
		if m.Enabled() {
			delete(doc, "enabled")
		} else {
			doc["enabled"] = false
		}

		return vlatte.DescribeRoot(doc)
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

	if m.delay != nil {
		doc["delay"] = delay.Doc(m.delay)
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

		if d.scenario[3] != "" {
			sc["key"] = d.scenario[3]
		}

		doc["scenario"] = sc
	}

	events, err := describeEvents(m.events)
	if err != nil {
		doc["on_complete"] = vlatte.DescribeFail("on_complete", err)
		return vlatte.DescribeRoot(doc)
	}

	if len(events) > 0 {
		doc["on_complete"] = events
	}

	req, err := d.describeRequest(m.method)
	if err != nil {
		doc["request"] = vlatte.DescribeFail("request", err)
		return vlatte.DescribeRoot(doc)
	}

	doc["request"] = req

	key, res, err := describeReply(m.reply)
	if err != nil {
		doc[key] = vlatte.DescribeFail(key, err)
		return vlatte.DescribeRoot(doc)
	}

	doc[key] = res

	return vlatte.DescribeRoot(doc)
}

// describeReply describes reply under the key of a mock file that holds it, or returns why none can.
func describeReply(reply Reply) (string, map[string]any, error) {
	switch r := reply.(type) {
	case *Response:
		res, err := r.describe()
		return "response", res, err
	case *SequentialReply:
		res, err := r.describe()
		return "response_sequence", res, err
	case *RandomReply:
		res, err := r.describe()
		return "response_random", res, err
	case *BidiReply:
		res, err := r.describe()
		return "bidi", res, err
	case *ProxyReply:
		res, err := r.describe()
		return "proxy", res, err
	default:
		return "response", nil, notDescribed(reply)
	}
}

// notDescribed returns why a mock file can't hold reply, a reply of no key of its own.
func notDescribed(reply Reply) error {
	switch reply.(type) {
	case ReplierFunc:
		return errFuncNotDescribed
	case *EchoReply:
		return errEchoNotDescribed
	case sharedReply:
		return errSharedNotDescribed
	default:
		return fmt.Errorf("%w: %T", errReplyNotDescribed, reply)
	}
}

// describeRequest describes the request part of a mock of method, or returns why a mock file can't hold a matcher of
// it.
func (d *mockDescription) describeRequest(method string) (map[string]any, error) {
	if d.request.undescribable {
		return nil, errCallMatchesNotDescribed
	}

	req := map[string]any{"method": method}

	header, err := describeMatchers("header", d.request.header)
	if err != nil {
		return nil, err
	}

	if len(header) > 0 {
		req["header"] = header
	}

	fields, err := describeMatchers("fields", d.request.fields)
	if err != nil {
		return nil, err
	}

	if len(fields) > 0 {
		req["fields"] = fields
	}

	if d.request.body != nil {
		v, err := describeMatcher(d.request.body)
		if err != nil {
			return nil, fmt.Errorf("body: %w", err)
		}

		req["body"] = v
	}

	if len(d.request.custom) > 0 {
		custom := make([]any, 0, len(d.request.custom))
		for i, m := range d.request.custom {
			v, err := describeMatcher(m)
			if err != nil {
				return nil, fmt.Errorf("custom[%d]: %w", i, err)
			}

			custom = append(custom, v)
		}

		req["custom"] = custom
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
		v, err := describeMessage(d.request.message)
		if err != nil {
			return nil, fmt.Errorf("message: %w", err)
		}

		req["message"] = v
	case d.request.messages != nil:
		v, err := describeMessages(d.request.messages)
		if err != nil {
			return nil, fmt.Errorf("messages%w", err)
		}

		req["messages"] = v
	}

	return req, nil
}

// describeMatchers describes the matchers of key by name, or returns why a mock file can't hold one.
func describeMatchers(key string, matchers []namedMatcher) (map[string]any, error) {
	out := make(map[string]any, len(matchers))

	for _, nm := range matchers {
		v, err := describeMatcher(nm.matcher)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", key, nm.name, err)
		}

		out[nm.name] = v
	}

	return out, nil
}

// describeMatcher returns the matcher list of m, as matchbuild reads it, or why a mock file can't hold m.
func describeMatcher(m vlatte.Matcher) (any, error) {
	d, ok := vlatte.IsSelfDescriptive(m)
	if !ok {
		return nil, errMatcherNotDescribed
	}

	v, err := d.Marshallable()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errMatcherNotDescribed, err)
	}

	return v, nil
}

// describeEvents describes the events that name a listener, or returns errCallbackNotDescribed for one that carries
// its own, as callbacks do.
func describeEvents(events vlatte.EventConfigs) ([]any, error) {
	var out []any

	for _, e := range events {
		if e.Listener != nil {
			return nil, fmt.Errorf("%w: %q", errCallbackNotDescribed, e.Name)
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

	return out, nil
}

// describeMessage returns msg as protojson writes it, as a JSON value, or why it can't, such as text that isn't
// UTF-8.
func describeMessage(msg proto.Message) (any, error) {
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return nil, err
	}

	var v any

	return v, json.Unmarshal(b, &v)
}

// describeMessages describes each of msgs, as describeMessage does.
func describeMessages(msgs []proto.Message) ([]any, error) {
	out := make([]any, 0, len(msgs))
	for i, msg := range msgs {
		v, err := describeMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("[%d]: %w", i, err)
		}

		out = append(out, v)
	}

	return out, nil
}

// describe describes the Response as a mock file's response, or returns why one cannot hold it.
func (r *Response) describe() (map[string]any, error) {
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
		msgs, err := describeMessages(r.messages)
		if err != nil {
			return nil, fmt.Errorf("messages%w", err)
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
		for i, d := range r.details {
			v, err := describeAny(d)
			if err != nil {
				return nil, fmt.Errorf("status.details[%d]: %w", i, err)
			}

			details = append(details, v)
		}

		if len(details) > 0 {
			st["details"] = details
		}

		res["status"] = st
	}

	if r.delay != nil {
		doc := delay.Doc(r.delay)
		if desc, ok := doc.(vlatte.Descriptor); ok {
			if err := desc.Validate(); err != nil {
				return nil, err
			}
		}
		res["message_delay"] = doc
	}

	return res, nil
}

// describe describes the SequentialReply as a mock file's response_sequence, or returns why one can't hold it: a reply
// of the sequence that isn't a Response mock files can hold.
func (r *SequentialReply) describe() (map[string]any, error) {
	responses, err := describeResponses(r.replies)
	if err != nil {
		return nil, err
	}

	res := map[string]any{"responses": responses}

	if r.replyAfterSequenceEnded != nil {
		after, err := describeResponses([]Reply{r.replyAfterSequenceEnded})
		if err != nil {
			return nil, fmt.Errorf("after_ended: %w", err)
		}

		res["after_ended"] = after[0]
	}

	return res, nil
}

// describe describes the RandomReply as a mock file's response_random, or returns why one can't hold it, as
// describeResponses does. RandWith's generator is left out: a mock file loads it back as a Rand of the same replies.
func (r *RandomReply) describe() (map[string]any, error) {
	responses, err := describeResponses(r.replies)
	if err != nil {
		return nil, err
	}

	res := map[string]any{"responses": responses}
	if r.seeded {
		res["seed"] = r.seed
	}

	return res, nil
}

// describeResponses describes replies, each a Response, as a mock file's list of responses, or returns why it can't:
// one isn't a Response, or can't be described.
func describeResponses(replies []Reply) ([]any, error) {
	out := make([]any, 0, len(replies))
	for i, reply := range replies {
		var (
			v   map[string]any
			err error
		)

		switch r := reply.(type) {
		case *Response:
			v, err = r.describe()
		case ReplierFunc, *EchoReply, sharedReply:
			err = notDescribed(r)
		default:
			err = fmt.Errorf("%w, not %T", errResponsesOnly, r)
		}

		if err != nil {
			return nil, fmt.Errorf("responses[%d]: %w", i, err)
		}

		out = append(out, v)
	}

	return out, nil
}

// describe describes the BidiReply as a mock file's bidi, or returns why one can't hold it.
func (b *BidiReply) describe() (map[string]any, error) {
	res := make(map[string]any)

	if h := describeMD(b.header); h != nil {
		res["header"] = h
	}

	if t := describeMD(b.trailer); t != nil {
		res["trailer"] = t
	}

	rules := make([]any, 0, len(b.rules))
	for i, rule := range b.rules {
		when, err := describeMatcher(rule.matcher)
		if err != nil {
			return nil, fmt.Errorf("on[%d].when: %w", i, err)
		}

		response, err := rule.response.describe()
		if err != nil {
			return nil, fmt.Errorf("on[%d].response: %w", i, err)
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

		v, err := r.describe()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}

		res[key] = v
	}

	return res, nil
}

// describe describes the ProxyReply as a mock file writes it, or returns errDialOptionsNotDescribed when it has dial
// options, which no mock file can hold.
func (r *ProxyReply) describe() (map[string]any, error) {
	if len(r.dialOptions) > 0 {
		return nil, errDialOptionsNotDescribed
	}

	res := map[string]any{"target": r.target}

	if h := describeMD(r.header); h != nil {
		res["header"] = h
	}

	if len(r.remove) > 0 {
		res["remove_headers"] = anys(r.remove)
	}

	return res, nil
}

// describeAny describes a status detail as an Any, with its @type, as mock files write it, or returns why it can't,
// such as an Any of a type the program doesn't have.
func describeAny(d proto.Message) (any, error) {
	b, err := protojson.Marshal(d)
	if err != nil {
		return nil, err
	}

	// protojson writes a message as a JSON object.
	var v map[string]any
	_ = json.Unmarshal(b, &v)

	if _, isAny := v["@type"]; !isAny {
		v["@type"] = "type.googleapis.com/" + string(d.ProtoReflect().Descriptor().FullName())
	}

	return v, nil
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
