package delay

import (
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
)

type spec struct {
	duration time.Duration
	lower    time.Duration
	upper    time.Duration
	latency  time.Duration
	jitter   time.Duration
	median   time.Duration
	sigma    float64
	maxValue time.Duration
}

type kind struct {
	schema   *config.Schema[spec]
	required []string
	build    func(spec) vlatte.Delay
}

type field = config.Field[spec]

var _kinds = map[string]kind{
	"fixed": {
		schema:   params(duration("duration", func(s *spec, d time.Duration) { s.duration = d })),
		required: []string{"duration"},
		build:    func(s spec) vlatte.Delay { return vlatte.Latency(s.duration) },
	},
	"uniform": {
		schema: params(
			duration("lower", func(s *spec, d time.Duration) { s.lower = d }),
			duration("upper", func(s *spec, d time.Duration) { s.upper = d }),
		),
		required: []string{"lower", "upper"},
		build:    func(s spec) vlatte.Delay { return vlatte.UniformDistribution(s.lower, s.upper) },
	},
	"jitter": {
		schema: params(
			duration("latency", func(s *spec, d time.Duration) { s.latency = d }),
			duration("jitter", func(s *spec, d time.Duration) { s.jitter = d }),
		),
		required: []string{"latency", "jitter"},
		build:    func(s spec) vlatte.Delay { return vlatte.Jitter(s.latency, s.jitter) },
	},
	"lognormal": {
		schema: params(
			duration("median", func(s *spec, d time.Duration) { s.median = d }),
			field{Key: "sigma", Kind: config.Any, Usage: "Finite nonnegative standard deviation of the natural logarithm.", Set: setSigma},
			duration("max_value", func(s *spec, d time.Duration) { s.maxValue = d }),
		),
		required: []string{"median", "sigma"},
		build:    func(s spec) vlatte.Delay { return vlatte.LogNormalDistribution(s.median, s.sigma, s.maxValue) },
	},
}

// Decode reads a duration shorthand or a typed distribution. Invalid parameters are config problems, never panics.
func Decode(v config.Value) (vlatte.Delay, error) {
	obj, object := v.Any.(map[string]any)
	if !object {
		obj = map[string]any{"type": "fixed", "duration": v.Any}
	}

	typ, ok := obj["type"].(string)
	k, known := _kinds[typ]
	if !ok || !known {
		if _, present := obj["type"]; !present {
			return nil, &config.Error{Problems: []config.Problem{{
				Code: config.CodeMissingValue, Origin: v.Origin, Key: v.Key + ".type",
				Message: v.Key + ".type is required", Hint: "use fixed, uniform, jitter or lognormal",
			}}}
		}

		return nil, config.Invalid(config.Value{Key: v.Key + ".type", Origin: v.Origin},
			fmt.Sprintf("%s.type must be fixed, uniform, jitter or lognormal, not %v", v.Key, obj["type"]),
			"write the type in lower case")
	}

	var problems []config.Problem
	for _, key := range k.required {
		if _, present := obj[key]; !present || obj[key] == nil {
			problems = append(problems, config.Problem{
				Code: config.CodeMissingValue, Origin: v.Origin, Key: v.Key + "." + key,
				Message: v.Key + "." + key + " is required by a " + typ + " delay",
			})
		}
	}

	// Schema declarations use snake_case. Adapt this one WireMock-compatible external parameter locally.
	obj = maps.Clone(obj)
	if _, present := obj["max_value"]; present {
		problems = append(problems, config.Problem{
			Code: config.CodeUnknownKey, Origin: v.Origin,
			Key: v.Key + ".max_value", Message: v.Key + ".max_value is not a known setting", Hint: "use maxValue",
		})
		delete(obj, "max_value")
	}

	if maxValue, present := obj["maxValue"]; present {
		obj["max_value"] = maxValue
		delete(obj, "maxValue")
	}

	var s spec
	err := k.schema.Decode(&s, config.Value{Key: v.Key, Origin: v.Origin, Any: obj})
	for _, p := range config.ProblemsOf(v, err) {
		if p.Key == v.Key+".max_value" {
			p.Key = v.Key + ".maxValue"
			p.Message = strings.Replace(p.Message, v.Key+".max_value", v.Key+".maxValue", 1)
		}
		p.Hint = strings.ReplaceAll(p.Hint, v.Key+".max_value?", v.Key+".maxValue?")
		p.Hint = strings.ReplaceAll(p.Hint, ", max_value", ", maxValue")
		if !object {
			p.Key = strings.TrimSuffix(p.Key, ".duration")
			p.Message = strings.ReplaceAll(p.Message, v.Key+".duration", v.Key)
		}

		problems = append(problems, p)
	}

	if len(problems) > 0 {
		return nil, &config.Error{Problems: problems}
	}

	return build(v, k, s)
}

func params(fields ...field) *config.Schema[spec] {
	return config.NewSchema(append([]field{{Key: "type", Kind: config.Any, Usage: "The delay distribution."}}, fields...)...)
}

func duration(key string, set func(*spec, time.Duration)) field {
	return field{
		Key: key, Kind: config.Duration, Usage: "Whole milliseconds or a Go duration, such as 1.5s.",
		Set: func(s *spec, v config.Value) error { set(s, v.Dur); return nil },
	}
}

func setSigma(s *spec, v config.Value) error {
	switch n := v.Any.(type) {
	case int64:
		s.sigma = float64(n)
	case float64:
		s.sigma = n
	default:
		return config.Mismatch(v, "a finite nonnegative number")
	}

	if s.sigma < 0 || math.IsNaN(s.sigma) || math.IsInf(s.sigma, 0) {
		return config.Invalid(v, fmt.Sprintf("%s must be a finite nonnegative number, not %v", v.Key, v.Any), "use 0 or a positive finite number")
	}

	return nil
}

// build follows faultbuild's boundary adapter: only the constructors' programmer-input panics become problems.
func build(v config.Value, k kind, s spec) (d vlatte.Delay, err error) {
	defer func() {
		if p := recover(); p != nil {
			message, ok := p.(string)
			if !ok || !strings.HasPrefix(message, "delay: ") {
				panic(p)
			}

			err = config.Invalid(v, v.Key+": "+strings.TrimPrefix(message, "delay: "), "")
		}
	}()

	return k.build(s), nil
}
