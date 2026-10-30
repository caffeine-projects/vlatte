package faultbuild

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

var _ vlatte.Stage = (*timedSink)(nil)

// timedSink logs each write, flush and close it gets, with the time it came, from start.
type timedSink struct {
	start time.Time
	log   []string
}

func (s *timedSink) Write(b []byte) (int, error) {
	s.log = append(s.log, string(b)+" at "+time.Since(s.start).String())
	return len(b), nil
}

func (s *timedSink) Flush() error {
	s.log = append(s.log, "flush at "+time.Since(s.start).String())
	return nil
}

func (s *timedSink) Close() error {
	s.log = append(s.log, "close at "+time.Since(s.start).String())
	return nil
}

// described returns how pipe describes itself, as a mock file holds it once read back.
func described(t *testing.T, pipe vlatte.Pipe) any {
	t.Helper()

	d, ok := vlatte.IsSelfDescriptive(pipe)
	require.True(t, ok, "%T doesn't describe itself", pipe)

	doc, err := value.ViaJSON(d)
	require.NoError(t, err)

	return doc
}

// obj returns the object of a spec from its keys and values, as a mock file holds it once read.
func obj(kv ...any) map[string]any {
	o := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		o[kv[i].(string)] = kv[i+1]
	}

	return o
}

func TestBuild_BuildsTheFaultItsSpecNames(t *testing.T) {
	// A mock file's spec builds the fault Go code builds with the same arguments, and the fault describes itself as
	// the spec that builds it again, so a mock described by the admin API, or kept by its store, loads back.
	tests := []struct {
		name string
		spec map[string]any
		want vlatte.Pipe
		desc map[string]any
	}{
		{
			name: "empty response", spec: obj("type", "empty_response"),
			want: fault.EmptyResponse(), desc: obj("type", "empty_response"),
		},
		{
			name: "malformed chunk", spec: obj("type", "malformed_response_chunk"),
			want: fault.MalformedResponseChunk(), desc: obj("type", "malformed_response_chunk"),
		},
		{
			name: "random data", spec: obj("type", "random_data_then_close"),
			want: fault.RandomDataThenClose(), desc: obj("type", "random_data_then_close"),
		},
		{
			name: "reset", spec: obj("type", "connection_reset_by_peer"),
			want: fault.ConnectionResetByPeer(), desc: obj("type", "connection_reset_by_peer"),
		},
		{
			name: "reset after", spec: obj("type", "connection_reset_by_peer_after", "after", "1.5s"),
			want: fault.ConnectionResetByPeerAfter(1500 * time.Millisecond),
			desc: obj("type", "connection_reset_by_peer_after", "after", "1.5s"),
		},
		{
			name: "dribble", spec: obj("type", "chunked_dribble_delay", "chunks", int64(5), "total", "2s"),
			want: fault.ChunkedDribbleDelay(5, 2*time.Second),
			desc: obj("type", "chunked_dribble_delay", "chunks", int64(5), "total", "2s"),
		},
		{
			name: "bandwidth", spec: obj("type", "bandwidth", "rate", int64(2000)),
			want: fault.Bandwidth(2000), desc: obj("type", "bandwidth", "rate", int64(2000)),
		},
		{
			name: "a common link", spec: obj("type", "bandwidth", "rate", "3g"),
			want: fault.Bandwidth(fault.Bandwidth3G), desc: obj("type", "bandwidth", "rate", int64(fault.Bandwidth3G)),
		},
		{
			name: "slicer", spec: obj("type", "slicer", "average_size", int64(64), "size_variation", int64(16), "delay", "10ms"),
			want: fault.Slicer(64, 16, 10*time.Millisecond),
			desc: obj("type", "slicer", "average_size", int64(64), "size_variation", int64(16), "delay", "10ms"),
		},
		{
			name: "a slicer by its size alone", spec: obj("type", "slicer", "average_size", int64(64)),
			want: fault.Slicer(64, 0, 0), desc: obj("type", "slicer", "average_size", int64(64)),
		},
		{
			name: "slow close", spec: obj("type", "slow_close", "delay", "2s"),
			want: fault.SlowClose(2 * time.Second), desc: obj("type", "slow_close", "delay", "2s"),
		},
		{
			name: "timeout", spec: obj("type", "timeout", "after", "3s"),
			want: fault.Timeout(3 * time.Second), desc: obj("type", "timeout", "after", "3s"),
		},
		{name: "a timeout that never ends", spec: obj("type", "timeout"), want: fault.Timeout(0), desc: obj("type", "timeout")},
		{
			name: "a timeout of 0", spec: obj("type", "timeout", "after", int64(0)),
			want: fault.Timeout(0), desc: obj("type", "timeout"),
		},
		{
			name: "limit data", spec: obj("type", "limit_data", "bytes", int64(0)),
			want: fault.LimitData(0), desc: obj("type", "limit_data", "bytes", int64(0)),
		},
		{
			name: "the most milliseconds", spec: obj("type", "timeout", "after", int64(9223372036854)),
			want: fault.Timeout(9223372036854 * time.Millisecond),
			desc: obj("type", "timeout", "after", (9223372036854 * time.Millisecond).String()),
		},
		{
			name: "milliseconds", spec: obj("type", "slow_close", "delay", int64(1500)),
			want: fault.SlowClose(1500 * time.Millisecond), desc: obj("type", "slow_close", "delay", "1.5s"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Build(tc.spec)
			require.NoError(t, err)
			require.Equal(t, tc.desc, described(t, pipe))
			require.Equal(t, described(t, tc.want), described(t, pipe))

			again, err := Build(described(t, pipe))
			require.NoError(t, err)
			require.Equal(t, tc.desc, described(t, again))
		})
	}
}

func TestBuild_LatencyDelaysAsItsSpecSays(t *testing.T) {
	// A latency alone delays every write by it; a jitter spreads the delays around it, from latency-jitter to
	// latency+jitter. Each describes itself as its spec.
	tests := []struct {
		name string
		spec map[string]any
		desc map[string]any
		from time.Duration
		to   time.Duration
	}{
		{
			name: "a latency", spec: obj("type", "latency", "delay", "500ms"), desc: obj("type", "latency", "delay", "500ms"),
			from: 500 * time.Millisecond, to: 500 * time.Millisecond,
		},
		{
			name: "a latency and its jitter", spec: obj("type", "latency", "delay", obj("type", "jitter", "latency", int64(1000), "jitter", "20ms")),
			desc: obj("type", "latency", "delay", obj("type", "jitter", "latency", "1s", "jitter", "20ms")),
			from: 980 * time.Millisecond, to: 1020 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Build(tc.spec)
			require.NoError(t, err)
			require.Equal(t, tc.desc, described(t, pipe))

			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Pipe{pipe})

				_, err := flow.Write([]byte("a"))
				require.NoError(t, err)
				require.NoError(t, flow.Close())

				require.Len(t, sink.log, 2)

				at, err := time.ParseDuration(sink.log[0][len("a at "):])
				require.NoError(t, err)
				require.GreaterOrEqual(t, at, tc.from)
				require.LessOrEqual(t, at, tc.to)
			})
		})
	}
}

func TestBuild_RefusesASpecItCantBuild(t *testing.T) {
	// A spec that is wrong fails as the mock file loads, with every problem keyed as the file writes it and a hint to
	// fix it where there is one, never with a panic.
	const types = "empty_response, malformed_response_chunk, random_data_then_close, connection_reset_by_peer, " +
		"chunked_dribble_delay, latency, bandwidth, slicer, slow_close, timeout, limit_data or connection_reset_by_peer_after"

	tests := []struct {
		name string
		spec any
		want []string
	}{
		{
			name: "a name alone", spec: "empty_response",
			want: []string{"fault: invalid_value: fault must be an object, such as {type: latency, delay: 300ms}, not a string"},
		},
		{
			name: "a list", spec: []any{"latency", "1s"},
			want: []string{"fault: invalid_value: fault must be an object, such as {type: latency, delay: 300ms}, not a list"},
		},
		{
			name: "no type", spec: obj("latency", "1s"),
			want: []string{"fault: missing_value: fault.type is required; name the fault, such as type: latency"},
		},
		{
			name: "an unknown type", spec: obj("type", "nope"),
			want: []string{`fault: invalid_value: fault.type must be one of ` + types + `, not "nope"`},
		},
		{
			name: "a type misspelled", spec: obj("type", "latancy"),
			want: []string{`fault: invalid_value: fault.type must be one of ` + types + `, not "latancy"; did you mean latency?`},
		},
		{
			name: "a type in another case", spec: obj("type", "Latency", "latency", "1s"),
			want: []string{`fault: invalid_value: fault.type must be latency, not "Latency"; write the type in lower case`},
		},
		{
			name: "a type of another kind", spec: obj("type", nil),
			want: []string{"fault: invalid_value: fault.type must be one of " + types + ", not null"},
		},
		{
			name: "a parameter missing", spec: obj("type", "latency"),
			want: []string{"fault: missing_value: fault.delay is required by a latency fault"},
		},
		{
			name: "every parameter missing", spec: obj("type", "chunked_dribble_delay"),
			want: []string{
				"fault: missing_value: fault.chunks is required by a chunked_dribble_delay fault",
				"fault: missing_value: fault.total is required by a chunked_dribble_delay fault",
			},
		},
		{
			name: "a parameter misspelled", spec: obj("type", "latency", "dely", "1s"),
			want: []string{
				"fault: missing_value: fault.delay is required by a latency fault",
				"fault: unknown_key: fault.dely is not a known setting; did you mean fault.delay?",
			},
		},
		{
			name: "a parameter the fault doesn't take", spec: obj("type", "empty_response", "delay", "1s"),
			want: []string{"fault: unknown_key: fault.delay is not a known setting; the keys under fault are type"},
		},
		{
			name: "a quoted number", spec: obj("type", "slow_close", "delay", "1500"),
			want: []string{`fault: invalid_value: fault.delay must be milliseconds or a Go duration, not "1500"; ` +
				`write 1500 without quotes for milliseconds, or add a unit, such as "1500ms"`},
		},
		{
			name: "a negative duration", spec: obj("type", "timeout", "after", "-1s"),
			want: []string{`fault: invalid_value: fault.after must be 0 or more, not "-1s"`},
		},
		{
			name: "negative milliseconds", spec: obj("type", "connection_reset_by_peer_after", "after", int64(-1)),
			want: []string{"fault: invalid_value: fault.after must be 0 or more, not -1"},
		},
		{
			name: "a duration of another type", spec: obj("type", "latency", "delay", true),
			want: []string{"fault: invalid_value: fault.delay must be milliseconds or a Go duration, not a bool"},
		},
		{
			name: "past the most milliseconds", spec: obj("type", "slow_close", "delay", int64(9223372036855)),
			want: []string{"fault: invalid_value: fault.delay must be at most 9223372036854 milliseconds, not 9223372036855"},
		},
		{
			name: "a fraction", spec: obj("type", "limit_data", "bytes", 1.5),
			want: []string{"fault: invalid_value: fault.bytes must be an integer, not 1.5"},
		},
		{
			name: "a count as text", spec: obj("type", "limit_data", "bytes", "10"),
			want: []string{"fault: invalid_value: fault.bytes must be an integer, not a string; write it without quotes: 10"},
		},
		{
			name: "a negative limit", spec: obj("type", "limit_data", "bytes", int64(-1)),
			want: []string{"fault: invalid_value: fault: limit data can't be -1 bytes"},
		},
		{
			name: "a fraction of a byte", spec: obj("type", "bandwidth", "rate", 0.5),
			want: []string{"fault: invalid_value: fault.rate must be bytes per second or a link, such as 3g, not a number"},
		},
		{
			name: "no rate", spec: obj("type", "bandwidth", "rate", int64(0)),
			want: []string{"fault: invalid_value: fault: bandwidth needs a byte per second at least, not 0"},
		},
		{
			name: "an unknown link", spec: obj("type", "bandwidth", "rate", "5g"),
			want: []string{`fault: invalid_value: fault.rate must be bytes per second or a link, not "5g"; ` +
				"the links are gprs, dial_up, 2g, good_2g, 3g, good_3g, dsl, 4g, wifi or fiber"},
		},
		{
			name: "no chunk", spec: obj("type", "chunked_dribble_delay", "chunks", int64(0), "total", "1s"),
			want: []string{"fault: invalid_value: fault: chunked dribble delay needs a chunk at least, not 0"},
		},
		{
			name: "a variation as large as the size", spec: obj("type", "slicer", "average_size", int64(2), "size_variation", int64(2)),
			want: []string{"fault: invalid_value: fault: slicer size variation must be from 0 to 1, not 2"},
		},
		{
			name: "a jitter past the latency", spec: obj("type", "latency", "delay", obj("type", "jitter", "latency", "1s", "jitter", "2s")),
			want: []string{"fault: invalid_value: fault.delay: expected 0 <= jitter <= latency and a representable latency+jitter, not latency 1s and jitter 2s"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pipe, err := Build(tc.spec)
			require.Nil(t, pipe)

			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Equal(t, "faultbuild", cfgErr.Scope)

			got := make([]string, 0, len(cfgErr.Problems))
			for _, p := range cfgErr.Problems {
				got = append(got, p.String())
			}

			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func TestBuildAt_KeysItsProblemsAsTheFileDoes(t *testing.T) {
	// A mock file lists faults under a key, such as pipes: each problem names the file and the fault's place in it,
	// so the user finds the line to fix.
	_, err := BuildAt(obj("type", "slicer", "size_variation", int64(1)), "file mocks/users.yaml", "pipes[2]")

	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, []config.Problem{{
		Code: config.CodeMissingValue, Origin: "file mocks/users.yaml", Key: "pipes[2].average_size",
		Message: "pipes[2].average_size is required by a slicer fault",
	}}, cfgErr.Problems)
}

// genDuration draws a duration as a mock file writes one: whole milliseconds, or a Go duration.
func genDuration(t *rapid.T, label string) any {
	d := time.Duration(rapid.Int64Range(0, int64(time.Hour)).Draw(t, label))
	if rapid.Bool().Draw(t, label+" in milliseconds") {
		return int64(d / time.Millisecond)
	}

	return d.String()
}

// genSpec draws a spec of a fault that builds.
func genSpec() *rapid.Generator[map[string]any] {
	return rapid.Custom(func(t *rapid.T) map[string]any {
		switch rapid.IntRange(0, 11).Draw(t, "fault") {
		case 0:
			return obj("type", "empty_response")
		case 1:
			return obj("type", "malformed_response_chunk")
		case 2:
			return obj("type", "random_data_then_close")
		case 3:
			return obj("type", "connection_reset_by_peer")
		case 4:
			return obj("type", "connection_reset_by_peer_after", "after", genDuration(t, "after"))
		case 5:
			return obj("type", "chunked_dribble_delay", "chunks", rapid.Int64Range(1, 100).Draw(t, "chunks"), "total", genDuration(t, "total"))
		case 6:
			latency := time.Duration(rapid.Int64Range(0, int64(time.Hour)).Draw(t, "latency"))
			if rapid.Bool().Draw(t, "jitter") {
				jitter := time.Duration(rapid.Int64Range(0, int64(latency)).Draw(t, "jitter"))
				return obj("type", "latency", "delay", obj("type", "jitter", "latency", latency.String(), "jitter", jitter.String()))
			}

			return obj("type", "latency", "delay", genDuration(t, "latency"))
		case 7:
			if rapid.Bool().Draw(t, "a common link") {
				return obj("type", "bandwidth", "rate", rapid.SampledFrom(_bandwidthNames).Draw(t, "link"))
			}

			return obj("type", "bandwidth", "rate", rapid.Int64Range(1, 1<<40).Draw(t, "rate"))
		case 8:
			size := rapid.Int64Range(1, 1<<20).Draw(t, "size")
			spec := obj("type", "slicer", "average_size", size)
			if rapid.Bool().Draw(t, "a variation") {
				spec["size_variation"] = rapid.Int64Range(0, size-1).Draw(t, "variation")
			}

			if rapid.Bool().Draw(t, "a delay") {
				spec["delay"] = genDuration(t, "delay")
			}

			return spec
		case 9:
			return obj("type", "slow_close", "delay", genDuration(t, "delay"))
		case 10:
			if rapid.Bool().Draw(t, "never") {
				return obj("type", "timeout")
			}

			return obj("type", "timeout", "after", genDuration(t, "after"))
		default:
			return obj("type", "limit_data", "bytes", rapid.Int64Range(0, 1<<40).Draw(t, "bytes"))
		}
	})
}

func TestPropBuild_ADescriptionBuildsTheSameFault(t *testing.T) {
	// The admin API describes a mock, and its store writes the description to a file that must load as the same mock:
	// a fault built from its own description describes itself the same, through JSON or YAML as a file holds it.
	rapid.Check(t, func(t *rapid.T) {
		pipe, err := Build(genSpec().Draw(t, "spec"))
		require.NoError(t, err)

		first, err := value.ViaJSON(pipe.(vlatte.SelfDescriptive).Describe())
		require.NoError(t, err)

		// The admin store writes YAML, the admin API JSON.
		for _, format := range []string{"json", "yaml"} {
			data, err := config.Encode(first, format)
			require.NoError(t, err)

			read, err := config.Parse(data, format)
			require.NoError(t, err)

			again, err := Build(read)
			require.NoError(t, err)

			second, err := value.ViaJSON(again.(vlatte.SelfDescriptive).Describe())
			require.NoError(t, err)
			require.Equal(t, first, second, format)
		}
	})
}

func TestBuilt_ABugStillPanics(t *testing.T) {
	// Only the messages constructors panic with on input they refuse become errors: a bug, such as an index out of
	// range, must not pass for a spec that is wrong.
	require.PanicsWithError(t, "runtime error: index out of range [1] with length 0", func() {
		_, _ = built(func() vlatte.Pipe {
			var pipes []vlatte.Pipe
			return pipes[1]
		})
	})
}
