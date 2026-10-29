// Package faultbuild builds the faults of package [github.com/caffeine-projects/vlatte/fault] from the fault specs of
// mock files: an object whose type names the fault and whose other keys are its parameters, such as
// {"type": "latency", "latency": "300ms", "jitter": "60ms"}. Each fault describes itself as the spec that builds it
// again.
package faultbuild

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/internal/config"
)

// _durationUsage says how mock files write durations.
const _durationUsage = "Whole milliseconds or a Go duration, such as 1.5s"

// _bandwidths are the [fault.Bandwidth] constants by the names mock files give them.
var _bandwidths = map[string]int{
	"gprs":    fault.BandwidthGPRS,
	"dial_up": fault.BandwidthDialUp,
	"2g":      fault.Bandwidth2G,
	"good_2g": fault.BandwidthGood2G,
	"3g":      fault.Bandwidth3G,
	"good_3g": fault.BandwidthGood3G,
	"dsl":     fault.BandwidthDSL,
	"4g":      fault.Bandwidth4G,
	"wifi":    fault.BandwidthWiFi,
	"fiber":   fault.BandwidthFiber,
}

// _bandwidthNames are the keys of _bandwidths, in order of speed, for hints.
var _bandwidthNames = []string{"gprs", "dial_up", "2g", "good_2g", "3g", "good_3g", "dsl", "4g", "wifi", "fiber"}

// spec holds the parameters of a fault spec, those of every fault: each fault reads its own.
type spec struct {
	after         time.Duration
	total         time.Duration
	latency       time.Duration
	jitter        time.Duration
	delay         time.Duration
	chunks        int
	averageSize   int
	sizeVariation int
	bytes         int
	rate          int
}

// kind is a fault of mock files: its type, the schema of its parameters, those it requires, and how it is built.
type kind struct {
	name     string
	schema   *config.Schema[spec]
	required []string
	build    func(s spec) vlatte.Pipe
}

type field = config.Field[spec]

var _kinds = []kind{
	{name: "empty_response", schema: params(), build: func(spec) vlatte.Pipe { return fault.EmptyResponse() }},
	{name: "malformed_response_chunk", schema: params(), build: func(spec) vlatte.Pipe { return fault.MalformedResponseChunk() }},
	{name: "random_data_then_close", schema: params(), build: func(spec) vlatte.Pipe { return fault.RandomDataThenClose() }},
	{name: "connection_reset_by_peer", schema: params(), build: func(spec) vlatte.Pipe { return fault.ConnectionResetByPeer() }},
	{
		name: "chunked_dribble_delay", required: []string{"chunks", "total"},
		build: func(s spec) vlatte.Pipe { return fault.ChunkedDribbleDelay(s.chunks, s.total) },
		schema: params(
			whole("chunks", "How many parts the body is sent in.", func(s *spec, n int) { s.chunks = n }),
			duration("total", "When the body ends.", func(s *spec, d time.Duration) { s.total = d }),
		),
	},
	{
		name: "latency", required: []string{"latency"},
		build: func(s spec) vlatte.Pipe { return fault.Latency(s.latency, s.jitter) },
		schema: params(
			duration("latency", "How late each write arrives.", func(s *spec, d time.Duration) { s.latency = d }),
			duration("jitter", "How much the latency varies by, up to the latency. 0 by default.", func(s *spec, d time.Duration) { s.jitter = d }),
		),
	},
	{
		name: "bandwidth", required: []string{"rate"},
		build: func(s spec) vlatte.Pipe { return fault.Bandwidth(s.rate) },
		schema: params(field{
			Key: "rate", Kind: config.Any, Usage: "Bytes per second, or a link: " + listOf(_bandwidthNames) + ".",
			Set: setRate,
		}),
	},
	{
		name: "slicer", required: []string{"average_size"},
		build: func(s spec) vlatte.Pipe { return fault.Slicer(s.averageSize, s.sizeVariation, s.delay) },
		schema: params(
			whole("average_size", "How many bytes a slice has, on average.", func(s *spec, n int) { s.averageSize = n }),
			whole("size_variation", "How many bytes a slice varies by, below average_size. 0 by default.", func(s *spec, n int) { s.sizeVariation = n }),
			duration("delay", "How long to wait before each slice. 0 by default.", func(s *spec, d time.Duration) { s.delay = d }),
		),
	},
	{
		name: "slow_close", required: []string{"delay"},
		build: func(s spec) vlatte.Pipe { return fault.SlowClose(s.delay) },
		schema: params(
			duration("delay", "How long the end of the answer waits.", func(s *spec, d time.Duration) { s.delay = d }),
		),
	},
	{
		name:  "timeout",
		build: func(s spec) vlatte.Pipe { return fault.Timeout(s.after) },
		schema: params(
			duration("after", "When the answer ends. 0, the default, never ends it.", func(s *spec, d time.Duration) { s.after = d }),
		),
	},
	{
		name: "limit_data", required: []string{"bytes"},
		build: func(s spec) vlatte.Pipe { return fault.LimitData(s.bytes) },
		schema: params(
			whole("bytes", "How many bytes pass before the connection closes.", func(s *spec, n int) { s.bytes = n }),
		),
	},
	{
		name: "connection_reset_by_peer_after", required: []string{"after"},
		build: func(s spec) vlatte.Pipe { return fault.ConnectionResetByPeerAfter(s.after) },
		schema: params(
			duration("after", "When the connection is reset.", func(s *spec, d time.Duration) { s.after = d }),
		),
	},
}

// _types reads the type of a spec, the name of one of _kinds, as its index. It reads the name in any case, which
// kindOf refuses: a type is written as the fault describes itself.
var _types = config.NewSchema(config.Field[int64]{
	Key: "type", Kind: config.Int, Choices: choices(), Usage: "The fault.",
	Set: func(index *int64, v config.Value) error { *index = v.Int; return nil },
})

// Build builds the fault a spec of a mock file describes: an object whose type names the fault, and whose other keys
// are its parameters. Durations are whole milliseconds, or a Go duration such as "1.5s", from 0. The types, after
// the functions of package fault, and their parameters:
//
//	empty_response
//	malformed_response_chunk
//	random_data_then_close
//	connection_reset_by_peer
//	chunked_dribble_delay           chunks, total
//	latency                         latency, jitter (0 by default)
//	bandwidth                       rate: bytes per second, or gprs, dial_up, 2g, good_2g, 3g, good_3g, dsl, 4g,
//	                                wifi or fiber, the Bandwidth constants of package fault
//	slicer                          average_size, size_variation (0 by default), delay (0 by default)
//	slow_close                      delay
//	timeout                         after (0 by default)
//	limit_data                      bytes
//	connection_reset_by_peer_after  after
//
// A spec with problems, such as a missing parameter, a key the fault doesn't take, or a value its function of
// package fault would panic on, fails with an error that lists each, keyed as in fault.latency.
func Build(doc any) (vlatte.Pipe, error) {
	pipe, err := BuildAt(doc, "fault", "fault")
	if cfgErr, ok := errors.AsType[*config.Error](err); ok {
		cfgErr.Scope = "faultbuild"
	}

	return pipe, err
}

// BuildAt is like [Build], for the spec at key of a document from origin, such as pipes[0] of
// "file mocks/users.yaml": its problems name both.
func BuildAt(doc any, origin, key string) (vlatte.Pipe, error) {
	v := config.Value{Key: key, Origin: origin, Any: doc}

	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, config.Mismatch(v, "an object, such as {type: latency, latency: 300ms}")
	}

	k, err := kindOf(v, obj)
	if err != nil {
		return nil, err
	}

	var s spec
	problems := config.ProblemsOf(v, k.schema.Decode(&s, v))

	for _, param := range k.required {
		if _, set := obj[param]; !set {
			problems = append(problems, config.Problem{
				Code: config.CodeMissingValue, Origin: origin, Key: key + "." + param,
				Message: fmt.Sprintf("%s.%s is required by a %s fault", key, param, k.name),
			})
		}
	}

	if len(problems) > 0 {
		return nil, &config.Error{Problems: problems}
	}

	pipe, err := built(func() vlatte.Pipe { return k.build(s) })
	if err != nil {
		return nil, config.Invalid(v, fmt.Sprintf("%s: %s", key, strings.TrimPrefix(err.Error(), "fault: ")), "")
	}

	return pipe, nil
}

// kindOf reads the type of the spec obj, at v.
func kindOf(v config.Value, obj map[string]any) (kind, error) {
	typ, set := obj["type"]
	if !set {
		return kind{}, &config.Error{Problems: []config.Problem{{
			Code: config.CodeMissingValue, Origin: v.Origin, Key: v.Key + ".type",
			Message: v.Key + ".type is required", Hint: "name the fault, such as type: latency",
		}}}
	}

	var index int64
	if err := _types.Decode(&index, config.Value{Key: v.Key, Origin: v.Origin, Any: map[string]any{"type": typ}}); err != nil {
		return kind{}, err
	}

	k := _kinds[index]
	if typ != k.name {
		return kind{}, config.Invalid(config.Value{Key: v.Key + ".type", Origin: v.Origin, Any: typ},
			fmt.Sprintf("%s.type must be %s, not %q", v.Key, k.name, typ), "write the type in lower case")
	}

	return k, nil
}

// choices are the types of _kinds, each the choice of its index.
func choices() []config.Choice {
	c := make([]config.Choice, len(_kinds))
	for i, k := range _kinds {
		c[i] = config.Choice{Name: k.name, Value: int64(i)}
	}

	return c
}

// listOf lists names as text: "a, b or c".
func listOf(names []string) string {
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// params is the schema of the parameters of a fault: fields, and type.
func params(fields ...field) *config.Schema[spec] {
	return config.NewSchema(append([]field{{Key: "type", Kind: config.Any, Usage: "The fault."}}, fields...)...)
}

// duration is a parameter that takes a duration.
func duration(key, usage string, set func(s *spec, d time.Duration)) field {
	return field{
		Key: key, Kind: config.Duration, Usage: usage + " " + _durationUsage + ".",
		Set: func(s *spec, v config.Value) error { set(s, v.Dur); return nil },
	}
}

// whole is a parameter that takes a whole number. Its fault checks its range.
func whole(key, usage string, set func(s *spec, n int)) field {
	return field{
		Key: key, Kind: config.Int, Usage: usage,
		Set: func(s *spec, v config.Value) error { set(s, int(v.Int)); return nil },
	}
}

// setRate reads a rate of bandwidth: bytes per second, or the name of a link.
func setRate(s *spec, v config.Value) error {
	switch x := v.Any.(type) {
	case int64:
		s.rate = int(x)
	case string:
		rate, known := _bandwidths[x]
		if !known {
			return config.Invalid(v, fmt.Sprintf("%s must be bytes per second or a link, not %q", v.Key, x),
				"the links are "+listOf(_bandwidthNames))
		}

		s.rate = rate
	default:
		return config.Mismatch(v, "bytes per second or a link, such as 3g", config.Int)
	}

	return nil
}

// built returns what fault, a constructor of package fault, returns, or the error of the message it panics with on
// input it refuses: the constructors check their input only by panicking.
func built(fault func() vlatte.Pipe) (pipe vlatte.Pipe, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}

		msg, isMessage := r.(string)
		if !isMessage {
			panic(r)
		}

		err = errors.New(msg)
	}()

	return fault(), nil
}
