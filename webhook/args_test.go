package webhook_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

func TestArgsFromMap(t *testing.T) {
	// Parameters decode the way mock files do; a map built in code means what its JSON means.
	args, err := webhook.ArgsFromMap(map[string]any{
		"url":             "http://a",
		"method":          "POST",
		"header":          map[string]string{"A": "1"},
		"header_template": map[string]any{"B": "{{ .X }}"},
		"body_template":   "x",
	})

	require.NoError(t, err)
	require.Equal(t, &webhook.Args{
		URL:            "http://a",
		Method:         "POST",
		Header:         map[string]string{"A": "1"},
		HeaderTemplate: map[string]string{"B": "{{ .X }}"},
		BodyTemplate:   "x",
	}, args)
}

func TestArgsFromMap_ReportsEveryProblem(t *testing.T) {
	// Parameters used to fall back to defaults when a value had the wrong type, and log_level ignored the int64 of
	// mock files. Now each problem is reported, as in mock files, all at once.
	_, err := webhook.ArgsFromMap(map[string]any{
		"url":          "http://a",
		"url_template": "http://b",
		"body":         "x",
		"body_file":    "y",
		"method":       5,
		"header":       map[string]any{"X": 1},
		"log_level":    -1,
	})

	require.ErrorIs(t, err, webhook.ErrBadArg)
	require.EqualError(t, err, `webhook: bad argument: parameters: 5 problems
  on_complete: conflict: parameters.body_file is set with parameters.body; keep one of them
  on_complete: invalid_value: parameters.header.X must be a string, not a number; write it in quotes: "1"
  on_complete: unknown_key: parameters.log_level is not a known setting; `+
		`the keys under parameters are url, url_template, method, header, header_template, body, body_template, body_file, multipart, multipart_template
  on_complete: invalid_value: parameters.method must be a string, not a number; write it in quotes: "5"
  on_complete: conflict: parameters.url_template is set with parameters.url; keep one of them`)
}

func TestArgsFromMap_ValuesJSONCantWrite(t *testing.T) {
	// A map built in code that JSON can't write isn't parameters either.
	_, err := webhook.ArgsFromMap(map[string]any{"url": make(chan int)})
	require.ErrorIs(t, err, webhook.ErrBadArg)
}

func TestWebHook_RunNamesTheMockOfBadParameters(t *testing.T) {
	// Parameters are checked when the event runs, so the problem names the mock whose on_complete holds them.
	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	mock, err := mockhttp.Getf("/x").ID("users").Reply(mockhttp.OK()).Build(m)
	require.NoError(t, err)

	err = wh.Run(t.Context(), onComplete{params: map[string]any{"url": "http://a", "log_level": -1}, mock: mock})
	require.ErrorIs(t, err, webhook.ErrBadArg)
	require.ErrorContains(t, err, "mock users: unknown_key: parameters.log_level")
}

// takes reports whether the parameter key takes v: strings, and objects of strings for headers. A multipart object
// needs a type and parts, which no drawn value has.
func takes(key string, v any) bool {
	switch key {
	case "multipart", "multipart_template":
		return false
	case "header", "header_template":
		switch m := v.(type) {
		case map[string]string:
			return true
		case map[string]any:
			for _, x := range m {
				if _, ok := x.(string); !ok {
					return false
				}
			}

			return true
		}

		return false
	case "log_level", "ssl_verify":
		return false
	default:
		_, ok := v.(string)
		return ok
	}
}

func TestPropArgsFromMap_Types(t *testing.T) {
	// W1: a parameter is taken exactly when it has its type, as in mock files: strings, and objects of strings for
	// headers, whatever Go types code builds them with. Keys the webhook doesn't read are never taken.
	keys := []string{
		"url", "url_template", "method", "body", "body_template", "body_file", "header", "header_template",
		"multipart", "multipart_template", "log_level", "ssl_verify",
	}
	values := []any{
		nil, true, "x", "", 5, int64(5), 1.5, float32(0.5),
		[]string{"a"},
		[]any{"a"},
		map[string]any{"a": "b"},
		map[string]string{"a": "b"},
		map[string]any{"a": 1},
		make(map[string]any),
	}

	rapid.Check(t, func(t *rapid.T) {
		key := rapid.SampledFrom(keys).Draw(t, "key")
		v := rapid.SampledFrom(values).Draw(t, "value")

		args, err := webhook.ArgsFromMap(map[string]any{key: v})

		if !takes(key, v) {
			require.ErrorIs(t, err, webhook.ErrBadArg)
			require.ErrorContains(t, err, "parameters."+key)

			return
		}

		require.NoError(t, err)

		got := map[string]any{
			"url": args.URL, "url_template": args.URLTemplate, "method": args.Method, "body": args.Body,
			"body_template": args.BodyTemplate, "body_file": args.BodyFile, "header": args.Header, "header_template": args.HeaderTemplate,
		}

		switch x := v.(type) {
		case string:
			require.Equal(t, x, got[key])
		case map[string]string:
			require.Equal(t, x, got[key])
		case map[string]any:
			require.Len(t, got[key], len(x))
		}
	})
}
