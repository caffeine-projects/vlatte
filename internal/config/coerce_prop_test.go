package config

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/value"
)

// genDocScalar draws a value a document may hold where a scalar setting is expected, of any class:
// text that reads as other kinds, numbers at the edges, bools, null, lists and objects.
func genDocScalar() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), toAnyValue),
		rapid.Map(rapid.SampledFrom([]string{
			"api", "", "150", "-3", "1.5", "5.0", "t", "true", "FALSE", "yes", "1.5s", "0", "-1s", "+1s", "1500",
			"info", "INFO", "Debug", "inf", "verbose", "a, b", " info", "10x",
		}), toAnyValue),
		rapid.Map(rapid.Int64Range(-3, 3), toAnyValue),
		rapid.Map(rapid.Int64(), toAnyValue),
		rapid.Map(rapid.SampledFrom([]float64{1.5, -0.5, 1e20, math.Inf(1), math.Inf(-1), 1, 0}), toAnyValue),
		rapid.Just[any](_maxMilliseconds+1),
		rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"a", "", " b"}), 0, 3), func(items []string) any {
			list := make([]any, 0, len(items))
			for _, item := range items {
				list = append(list, item)
			}

			return list
		}),
		rapid.Just[any]([]any{"a", int64(1), true}),
		rapid.Just[any](map[string]any{"a": "b"}),
	)
}

func toAnyValue[T any](v T) any { return v }

// docOracle is what a document's raw value converts to for each setting of the test schema, written from the
// rules of the kinds rather than from coerce.
func docOracle(key string, raw any) (Value, bool) {
	switch key {
	case "name":
		s, ok := raw.(string)
		return Value{Str: s}, ok
	case "debug":
		switch x := raw.(type) {
		case bool:
			return Value{Bool: x}, true
		case string:
			b, err := strconv.ParseBool(x)
			return Value{Bool: b}, err == nil
		case int64:
			return Value{Bool: x == 1}, x == 0 || x == 1
		case float64:
			return Value{Bool: x == 1}, x == 0 || x == 1
		}
	case "port":
		n, ok := raw.(int64)
		return Value{Int: n}, ok
	case "log.level":
		s, ok := raw.(string)

		switch {
		case ok && strings.EqualFold(s, "debug"):
			return Value{Int: 0}, true
		case ok && strings.EqualFold(s, "info"):
			return Value{Int: 1}, true
		}
	case "log.timeout":
		switch x := raw.(type) {
		case int64:
			return Value{Dur: time.Duration(x) * time.Millisecond}, x >= 0 && x <= math.MaxInt64/int64(time.Millisecond)
		case string:
			d, err := time.ParseDuration(x)
			return Value{Dur: d}, err == nil && !strings.HasPrefix(x, "-") && !strings.HasPrefix(x, "+")
		}
	case "tags":
		list, ok := raw.([]any)
		if !ok {
			return Value{}, false
		}

		items := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return Value{}, false
			}

			items = append(items, s)
		}

		return Value{List: items}, true
	}

	return Value{}, false
}

func TestPropCoerce_DocumentTypes(t *testing.T) {
	// C11: a document's value is taken exactly when its kind takes its type, whatever its class, and every
	// rejection reads "<key> must be ...", with the hint for the usual slips.
	s := testSchema()

	rapid.Check(t, func(t *rapid.T) {
		key := rapid.SampledFrom([]string{"name", "debug", "port", "log.level", "log.timeout", "tags"}).Draw(t, "key")
		raw := genDocScalar().Draw(t, "raw")

		want, ok := docOracle(key, raw)
		v, problems := coerce(s.byKey[key], entry{key: key, raw: raw, origin: "file a.yaml"}, key)

		require.Equal(t, ok, len(problems) == 0, "%s: %#v", key, raw)

		if ok {
			require.Equal(t, want.Str, v.Str)
			require.Equal(t, want.Bool, v.Bool)
			require.Equal(t, want.Int, v.Int)
			require.Equal(t, want.Dur, v.Dur)
			require.Equal(t, want.List, v.List)

			return
		}

		for _, p := range problems {
			require.Equal(t, CodeInvalidValue, p.Code)
			require.Equal(t, "file a.yaml", p.Origin)
			require.True(t, strings.HasPrefix(p.Message, p.Key+" must be "), p.Message)
			require.True(t, p.Key == key || strings.HasPrefix(p.Key, key+"["), p.Key)
		}

		hint := problems[0].Hint

		switch x := raw.(type) {
		case bool, int64, float64:
			if key == "name" {
				require.Equal(t, "write it in quotes: "+strconv.Quote(rawText(x)), hint)
			}
		case string:
			if n, isNumber := value.ParseNumber(x); isNumber && key == "port" {
				if _, isInt := n.(int64); isInt {
					require.Equal(t, "write it without quotes: "+x, hint)
				}
			}

			if key == "tags" {
				require.True(t, strings.HasPrefix(hint, "write a list: ["), hint)
			}
		}
	})
}

// textOracle is what text from env or args converts to for each setting of the nested schema, written from the
// rules of the kinds rather than from coerce.
func textOracle(key, s string) (nestedDoc, bool) {
	switch key {
	case "title":
		return nestedDoc{Title: s}, true
	case "on":
		b, err := strconv.ParseBool(s)
		return nestedDoc{On: b}, err == nil
	case "count":
		n, ok := value.ParseNumber(s)
		i, isInt := n.(int64)

		return nestedDoc{Count: i}, ok && isInt
	case "wait":
		if n, ok := value.ParseNumber(s); ok {
			ms, isInt := n.(int64)
			return nestedDoc{Wait: time.Duration(ms) * time.Millisecond}, isInt && ms >= 0 && ms <= _maxMilliseconds
		}

		d, err := time.ParseDuration(s)

		return nestedDoc{Wait: d}, err == nil && !strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "+")
	default:
		items := []string{}
		for item := range strings.SplitSeq(s, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}

		return nestedDoc{Tags: items}, true
	}
}

func TestPropCoerce_TextSources(t *testing.T) {
	// C12: env vars and flags read text alike, as the oracle says: bools as strconv.ParseBool reads them,
	// numbers in the JSON grammar, durations as milliseconds or unsigned Go durations, lists comma-separated.
	// A rejected value gets the same problem from both, but for its origin.
	outer := t

	rapid.Check(t, func(t *rapid.T) {
		key := rapid.SampledFrom([]string{"title", "on", "count", "wait", "tags"}).Draw(t, "key")
		text := rapid.OneOf(
			rapid.SampledFrom([]string{
				"1", "0", "t", "True", "yes", " 1", "5", "5.0", "+5", "05", "1e2", "1.5", "-3", "1e19",
				"1500", "1.5s", "-1s", "+1s", "0", "10x", "a, b,,c", ",", "x",
			}),
			rapid.StringMatching(`[-+ 0-9a-z.,]{1,8}`),
		).Draw(t, "text")

		env := "NEST_" + strings.ToUpper(key)
		outer.Setenv(env, text)

		defer outer.Setenv(env, "")

		fromEnv, _, envErr := loadNested(Env("NEST"))
		fromArgs, _, argsErr := loadNested(Args([]string{"--" + key + "=" + text}))

		want, ok := textOracle(key, text)
		if ok {
			require.NoError(t, envErr)
			require.NoError(t, argsErr)
			require.Equal(t, want, fromEnv, "%s=%q", key, text)
			require.Equal(t, want, fromArgs, "%s=%q", key, text)

			return
		}

		envProblems, argsProblems := requireProblems(t, envErr), requireProblems(t, argsErr)
		require.Len(t, envProblems, 1)
		require.Len(t, argsProblems, 1)

		envProblems[0].Origin, argsProblems[0].Origin = "", ""
		require.Equal(t, envProblems, argsProblems)
		require.True(t, strings.HasPrefix(envProblems[0].Message, key+" must be "), envProblems[0].Message)
	})
}
