package fault_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

// described returns how step describes itself, as a mock file holds it once read back.
func described(t *testing.T, step vlatte.Step) any {
	t.Helper()

	d, ok := descriptor.IsSelfDescriptive(step)
	require.True(t, ok, "%T doesn't describe itself", step)

	doc, err := value.ViaJSON(d)
	require.NoError(t, err)

	return doc
}

func TestBuild_BuildsTheFaultItsSpecNames(t *testing.T) {
	// A mock file's spec builds the fault Go code builds with the same arguments, and the fault describes itself as
	// the spec that builds it again, so a mock described by the admin API, or kept by its store, loads back.
	tests := []struct {
		name string
		spec any
		want vlatte.Step
		desc []any
	}{
		{name: "a name alone", spec: "empty_response", want: fault.EmptyResponse(), desc: []any{"empty_response"}},
		{name: "empty response", spec: []any{"empty_response"}, want: fault.EmptyResponse(), desc: []any{"empty_response"}},
		{name: "malformed chunk", spec: []any{"malformed_response_chunk"}, want: fault.MalformedResponseChunk(), desc: []any{"malformed_response_chunk"}},
		{name: "random data", spec: []any{"random_data_then_close"}, want: fault.RandomDataThenClose(), desc: []any{"random_data_then_close"}},
		{name: "reset", spec: []any{"connection_reset_by_peer"}, want: fault.ConnectionResetByPeer(), desc: []any{"connection_reset_by_peer"}},
		{
			name: "reset after", spec: []any{"connection_reset_by_peer_after", "1.5s"},
			want: fault.ConnectionResetByPeerAfter(1500 * time.Millisecond), desc: []any{"connection_reset_by_peer_after", "1.5s"},
		},
		{
			name: "dribble", spec: []any{"chunked_dribble_delay", int64(5), "2s"},
			want: fault.ChunkedDribbleDelay(5, 2*time.Second), desc: []any{"chunked_dribble_delay", int64(5), "2s"},
		},
		{name: "bandwidth", spec: []any{"bandwidth", int64(2000)}, want: fault.Bandwidth(2000), desc: []any{"bandwidth", int64(2000)}},
		{name: "a common link", spec: []any{"bandwidth", "3g"}, want: fault.Bandwidth(fault.Bandwidth3G), desc: []any{"bandwidth", int64(fault.Bandwidth3G)}},
		{
			name: "slicer", spec: []any{"slicer", int64(64), int64(16), "10ms"},
			want: fault.Slicer(64, 16, 10*time.Millisecond), desc: []any{"slicer", int64(64), int64(16), "10ms"},
		},
		{name: "slow close", spec: []any{"slow_close", "2s"}, want: fault.SlowClose(2 * time.Second), desc: []any{"slow_close", "2s"}},
		{name: "timeout", spec: []any{"timeout", "0s"}, want: fault.Timeout(0), desc: []any{"timeout", "0s"}},
		{name: "limit data", spec: []any{"limit_data", int64(0)}, want: fault.LimitData(0), desc: []any{"limit_data", int64(0)}},
		{
			name: "the most milliseconds", spec: []any{"timeout", int64(9223372036854)},
			want: fault.Timeout(9223372036854 * time.Millisecond), desc: []any{"timeout", (9223372036854 * time.Millisecond).String()},
		},
		{name: "milliseconds", spec: []any{"slow_close", int64(1500)}, want: fault.SlowClose(1500 * time.Millisecond), desc: []any{"slow_close", "1.5s"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step, err := fault.Build(tc.spec)
			require.NoError(t, err)
			require.Equal(t, tc.desc, described(t, step))
			require.Equal(t, described(t, tc.want), described(t, step))

			again, err := fault.Build(described(t, step))
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
		spec []any
		desc []any
		from time.Duration
		to   time.Duration
	}{
		{
			name: "a latency", spec: []any{"latency", "500ms"}, desc: []any{"latency", "500ms"},
			from: 500 * time.Millisecond, to: 500 * time.Millisecond,
		},
		{
			name: "a latency and its jitter", spec: []any{"latency", int64(1000), "20ms"}, desc: []any{"latency", "1s", "20ms"},
			from: 980 * time.Millisecond, to: 1020 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step, err := fault.Build(tc.spec)
			require.NoError(t, err)
			require.Equal(t, tc.desc, described(t, step))

			synctest.Test(t, func(t *testing.T) {
				sink := &timedSink{start: time.Now()}
				flow := vlatte.NewFlow(t.Context(), sink, []vlatte.Step{step})

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
	// A spec that is wrong fails as the mock file loads, with what is wrong, never with a panic.
	tests := []struct {
		name string
		spec any
		want string
	}{
		{name: "no spec", spec: []any{}, want: `fault: a fault spec names its fault first, as in ["latency", "100ms"]`},
		{name: "an object", spec: map[string]any{"latency": "1s"}, want: `fault: a fault is a name or a spec such as ["latency", "100ms"], not an object`},
		{name: "a number first", spec: []any{int64(1)}, want: `fault: a fault spec names its fault first, as in ["latency", "100ms"], not a number`},
		{name: "an unknown fault", spec: "nope", want: `fault: unknown fault "nope"`},
		{name: "names keep their case", spec: "Empty_Response", want: `fault: unknown fault "Empty_Response"`},
		{name: "an argument too many", spec: []any{"empty_response", int64(1)}, want: "fault: empty_response takes 0 arguments, not 1"},
		{name: "an argument short", spec: []any{"slicer", int64(64), int64(16)}, want: "fault: slicer takes 3 arguments, not 2"},
		{name: "a duration alone", spec: "slow_close", want: "fault: slow_close takes 1 argument, not 0"},
		{
			name: "a quoted number", spec: []any{"slow_close", "1500"},
			want: "fault: slow_close takes a duration from 0, whole milliseconds or a Go duration such as 1.5s, not 1500",
		},
		{
			name: "a negative duration", spec: []any{"timeout", "-1s"},
			want: "fault: timeout takes a duration from 0, whole milliseconds or a Go duration such as 1.5s, not -1s",
		},
		{
			name: "negative milliseconds", spec: []any{"connection_reset_by_peer_after", int64(-1)},
			want: "fault: connection_reset_by_peer_after takes a duration from 0, whole milliseconds or a Go duration such as 1.5s, not -1",
		},
		{
			name: "a duration of another type", spec: []any{"latency", true},
			want: "fault: latency takes a latency, whole milliseconds or a Go duration such as 1.5s, not a bool",
		},
		{
			name: "the most milliseconds", spec: []any{"slow_close", int64(9223372036855)},
			want: "fault: slow_close takes a duration of at most 9223372036854 milliseconds, not 9223372036855",
		},
		{name: "a fraction", spec: []any{"limit_data", 1.5}, want: "fault: limit_data takes a number of bytes, a whole number, not a number"},
		{name: "a count as text", spec: []any{"limit_data", "10"}, want: "fault: limit_data takes a number of bytes, a whole number, not a string"},
		{name: "a negative limit", spec: []any{"limit_data", int64(-1)}, want: "fault: limit data can't be -1 bytes"},
		{name: "a fraction of a byte", spec: []any{"bandwidth", 0.5}, want: "fault: bandwidth takes bytes per second, a whole number, not a number"},
		{name: "a dribble without its total", spec: []any{"chunked_dribble_delay", int64(3)}, want: "fault: chunked_dribble_delay takes 2 arguments, not 1"},
		{
			name: "chunks as text", spec: []any{"chunked_dribble_delay", "3", "1s"},
			want: "fault: chunked_dribble_delay takes chunks, a whole number, not a string",
		},
		{
			name: "a total of another type", spec: []any{"chunked_dribble_delay", int64(3), false},
			want: "fault: chunked_dribble_delay takes total, whole milliseconds or a Go duration such as 1.5s, not a bool",
		},
		{
			name: "a slice size as text", spec: []any{"slicer", "64", int64(16), "0s"},
			want: "fault: slicer takes the average size, a whole number, not a string",
		},
		{
			name: "a size variation as text", spec: []any{"slicer", int64(64), "16", "0s"},
			want: "fault: slicer takes the size variation, a whole number, not a string",
		},
		{
			name: "a slicer delay of another type", spec: []any{"slicer", int64(64), int64(16), []any{}},
			want: "fault: slicer takes the delay, whole milliseconds or a Go duration such as 1.5s, not a list",
		},
		{name: "a latency alone", spec: "latency", want: "fault: latency takes 1 or 2 arguments, not 0"},
		{name: "a limit alone", spec: "limit_data", want: "fault: limit_data takes 1 argument, not 0"},
		{name: "two rates", spec: []any{"bandwidth", int64(1), int64(2)}, want: "fault: bandwidth takes 1 argument, not 2"},
		{name: "no rate", spec: []any{"bandwidth", int64(0)}, want: "fault: bandwidth needs a byte per second at least, not 0"},
		{
			name: "an unknown link", spec: []any{"bandwidth", "5g"},
			want: `fault: bandwidth takes bytes per second or gprs, dial_up, 2g, good_2g, 3g, good_3g, dsl, 4g, wifi or fiber, not "5g"`,
		},
		{name: "no chunk", spec: []any{"chunked_dribble_delay", int64(0), "1s"}, want: "fault: chunked dribble delay needs a chunk at least, not 0"},
		{
			name: "a variation as large as the size", spec: []any{"slicer", int64(2), int64(2), "0s"},
			want: "fault: slicer size variation must be from 0 to 1, not 2",
		},
		{
			name: "a negative jitter", spec: []any{"latency", "1s", int64(-1)},
			want: "fault: latency takes a jitter from 0, whole milliseconds or a Go duration such as 1.5s, not -1",
		},
		{name: "a jitter past the latency", spec: []any{"latency", "1s", "2s"}, want: "fault: latency jitter must be from 0 to 1s, not 2s"},
		{name: "a jitter of a percent", spec: []any{"latency", "1s", 20.5}, want: "fault: latency takes a jitter, whole milliseconds or a Go duration such as 1.5s, not a number"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			step, err := fault.Build(tc.spec)
			require.EqualError(t, err, tc.want)
			require.Nil(t, step)
		})
	}
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
func genSpec() *rapid.Generator[any] {
	return rapid.Custom(func(t *rapid.T) any {
		switch rapid.IntRange(0, 11).Draw(t, "fault") {
		case 0:
			return rapid.SampledFrom([]any{"empty_response", []any{"empty_response"}}).Draw(t, "spec")
		case 1:
			return []any{"malformed_response_chunk"}
		case 2:
			return []any{"random_data_then_close"}
		case 3:
			return []any{"connection_reset_by_peer"}
		case 4:
			return []any{"connection_reset_by_peer_after", genDuration(t, "d")}
		case 5:
			return []any{"chunked_dribble_delay", rapid.Int64Range(1, 100).Draw(t, "chunks"), genDuration(t, "total")}
		case 6:
			latency := time.Duration(rapid.Int64Range(0, int64(time.Hour)).Draw(t, "latency"))
			if rapid.Bool().Draw(t, "jitter") {
				return []any{"latency", latency.String(), time.Duration(rapid.Int64Range(0, int64(latency)).Draw(t, "jitter")).String()}
			}

			return []any{"latency", genDuration(t, "latency")}
		case 7:
			if rapid.Bool().Draw(t, "a common link") {
				return []any{"bandwidth", rapid.SampledFrom([]string{"gprs", "dial_up", "2g", "good_2g", "3g", "good_3g", "dsl", "4g", "wifi", "fiber"}).Draw(t, "link")}
			}

			return []any{"bandwidth", rapid.Int64Range(1, 1<<40).Draw(t, "rate")}
		case 8:
			size := rapid.Int64Range(1, 1<<20).Draw(t, "size")
			return []any{"slicer", size, rapid.Int64Range(0, size-1).Draw(t, "variation"), genDuration(t, "delay")}
		case 9:
			return []any{"slow_close", genDuration(t, "delay")}
		case 10:
			return []any{"timeout", genDuration(t, "timeout")}
		default:
			return []any{"limit_data", rapid.Int64Range(0, 1<<40).Draw(t, "n")}
		}
	})
}

func TestPropBuild_ADescriptionBuildsTheSameFault(t *testing.T) {
	// The admin API describes a mock, and its store writes the description to a file that must load as the same mock:
	// a fault built from its own description describes itself the same, through JSON or YAML as a file holds it.
	rapid.Check(t, func(t *rapid.T) {
		step, err := fault.Build(genSpec().Draw(t, "spec"))
		require.NoError(t, err)

		first, err := value.ViaJSON(step.(descriptor.SelfDescriptive).Describe())
		require.NoError(t, err)

		// The admin store writes YAML, the admin API JSON.
		for _, format := range []string{"json", "yaml"} {
			data, err := config.Encode(first, format)
			require.NoError(t, err)

			read, err := config.Parse(data, format)
			require.NoError(t, err)

			again, err := fault.Build(read)
			require.NoError(t, err)

			second, err := value.ViaJSON(again.(descriptor.SelfDescriptive).Describe())
			require.NoError(t, err)
			require.Equal(t, first, second, format)
		}
	})
}
