package delay

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

func TestDecode_DescriptionsRoundTripThroughJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, d := range []vlatte.Delay{vlatte.Latency(0), vlatte.Latency(123), vlatte.UniformDistribution(10, 20), vlatte.Jitter(10, 2), vlatte.LogNormalDistribution(100, 0.5, 200)} {
			raw, err := json.Marshal(map[string]any{"delay": Doc(d)})
			require.NoError(t, err)
			plain, err := config.Parse(raw, "json")
			require.NoError(t, err)
			b, err := config.Encode(plain, format)
			require.NoError(t, err)
			obj, err := config.Parse(b, format)
			require.NoError(t, err)
			back, err := Decode(config.Value{Key: "delay", Origin: "test", Any: obj.(map[string]any)["delay"]})
			require.NoError(t, err)
			want, err := d.Describe().MarshalJSON()
			require.NoError(t, err)
			got, err := back.Describe().MarshalJSON()
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(got))
		}
	}
}

func TestDecode_InvalidFilesReturnProblemsInsteadOfPanicking(t *testing.T) {
	tests := []struct {
		name  string
		value any
		key   string
		code  string
	}{
		{"negative shorthand", int64(-1), "delay", config.CodeInvalidValue},
		{"fractional milliseconds", 1.5, "delay", config.CodeInvalidValue},
		{"unknown type", map[string]any{"type": "normal"}, "delay.type", config.CodeInvalidValue},
		{"missing type", map[string]any{"median": "1s"}, "delay.type", config.CodeMissingValue},
		{"missing sigma", map[string]any{"type": "lognormal", "median": "1s"}, "delay.sigma", config.CodeMissingValue},
		{"wrong casing", map[string]any{"type": "Lognormal", "median": "1s", "sigma": 1.0}, "delay.type", config.CodeInvalidValue},
		{"sigma text", map[string]any{"type": "lognormal", "median": "1s", "sigma": "1"}, "delay.sigma", config.CodeInvalidValue},
		{"sigma NaN", map[string]any{"type": "lognormal", "median": "1s", "sigma": math.NaN()}, "delay.sigma", config.CodeInvalidValue},
		{"sigma infinity", map[string]any{"type": "lognormal", "median": "1s", "sigma": math.Inf(1)}, "delay.sigma", config.CodeInvalidValue},
		{"zero median", map[string]any{"type": "lognormal", "median": "0s", "sigma": 1.0}, "delay", config.CodeInvalidValue},
		{"cutoff below median", map[string]any{"type": "lognormal", "median": "1s", "sigma": 1.0, "maxValue": "1ms"}, "delay", config.CodeInvalidValue},
		{"invalid cutoff type", map[string]any{"type": "lognormal", "median": "1s", "sigma": 1.0, "maxValue": false}, "delay.maxValue", config.CodeInvalidValue},
		{"snake case rejected", map[string]any{"type": "lognormal", "median": "1s", "sigma": 1.0, "max_value": "2s"}, "delay.max_value", config.CodeUnknownKey},
		{"unknown parameter", map[string]any{"type": "fixed", "duration": "1s", "extra": true}, "delay.extra", config.CodeUnknownKey},
		{"inverted uniform", map[string]any{"type": "uniform", "lower": "2s", "upper": "1s"}, "delay", config.CodeInvalidValue},
		{"overflowing jitter", map[string]any{"type": "jitter", "latency": time.Duration(math.MaxInt64).String(), "jitter": "1ns"}, "delay", config.CodeInvalidValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Decode(config.Value{Key: "delay", Origin: "test", Any: tt.value})
			require.Nil(t, d)
			problems, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "%v", err)
			require.Len(t, problems.Problems, 1)
			require.Equal(t, tt.key, problems.Problems[0].Key)
			require.Equal(t, tt.code, problems.Problems[0].Code)
			require.Equal(t, "test", problems.Problems[0].Origin)
		})
	}
}

func TestDecode_MaxValueKeepsItsPublicNameWithoutMutatingInput(t *testing.T) {
	obj := map[string]any{"type": "lognormal", "median": "1s", "sigma": int64(0), "maxValue": "2s"}
	d, err := Decode(config.Value{Key: "response.event_delay", Any: obj})
	require.NoError(t, err)
	n, err := d.Next(t.Context())
	require.NoError(t, err)
	require.Equal(t, time.Second, n)
	require.Equal(t, map[string]any{"type": "lognormal", "median": "1s", "sigma": int64(0), "maxValue": "2s"}, obj)
}

func TestDoc_OpaqueFunctionsFailWithoutBeingEvaluated(t *testing.T) {
	d := vlatte.DelayFunc(func(context.Context) (time.Duration, error) { panic("must not sample to describe") })
	_, err := json.Marshal(map[string]any{"delay": Doc(d)})
	require.ErrorIs(t, err, vlatte.ErrDelayNotDescribed)
}

func TestDecode_DiagnosticsPreserveExternalParameterNames(t *testing.T) {
	for _, tt := range []struct {
		key  string
		hint string
	}{
		{"maxValu", "the keys under delay are type, median, sigma, maxValue"},
		{"max_value_extra", "the keys under delay are type, median, sigma, maxValue"},
	} {
		t.Run(tt.key, func(t *testing.T) {
			_, err := Decode(config.Value{Key: "delay", Any: map[string]any{"type": "lognormal", "median": "1s", "sigma": 1.0, tt.key: "2s"}})
			cfg, ok := errors.AsType[*config.Error](err)
			require.True(t, ok)
			require.Len(t, cfg.Problems, 1)
			require.Equal(t, "delay."+tt.key, cfg.Problems[0].Key)
			require.Equal(t, "delay."+tt.key+" is not a known setting", cfg.Problems[0].Message)
			require.Equal(t, tt.hint, cfg.Problems[0].Hint)
		})
	}
}

func TestBuild_UnexpectedConstructorPanicsRemainVisible(t *testing.T) {
	for _, cause := range []any{errors.New("test: unexpected constructor panic"), "test: unexpected constructor panic"} {
		require.PanicsWithValue(t, cause, func() {
			_, _ = build(config.Value{Key: "delay"}, kind{
				build: func(spec) vlatte.Delay { panic(cause) },
			}, spec{})
		})
	}
}
