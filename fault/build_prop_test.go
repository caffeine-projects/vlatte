package fault_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/descriptor"
	"github.com/caffeine-projects/vlatte/fault"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

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
