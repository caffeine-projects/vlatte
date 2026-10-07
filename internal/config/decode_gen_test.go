package config

import (
	"math"
	"strings"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// _yamlTraps are strings that YAML reads as something else when unquoted: bools, nulls, numbers,
// timestamps, merge keys and indicators. Encoders must quote them, and decoding must give them back as text.
var _yamlTraps = []string{
	"yes", "no", "on", "off", "true", "False", "null", "~", "Null", "",
	"1", "-1", "0x1f", "0o17", "1_000", "1e3", ".inf", "-.Inf", ".nan", "1.5",
	"2024-01-01", "2024-01-01T10:20:30Z", "2001-12-14 21:59:43.10 -5",
	"<<", "- a", ": b", "#c", "&anchor", "*alias", "!tag", "%dir", "@at", "`tick",
	" lead", "trail ", "a: b", "a #b", "{x}", "[y]", "'q'", `"dq"`, "multi\nline", "tab\there",
}

// genText draws text, mixing YAML traps, multi-line text and arbitrary unicode.
func genText() *rapid.Generator[string] {
	return rapid.OneOf(rapid.SampledFrom(_yamlTraps), genLines(), rapid.String())
}

// genLines draws multi-line text such as indented JSON or HTML bodies: lines with leading and trailing
// spaces or tabs, blank lines and trailing line breaks, which YAML writes as blocks.
func genLines() *rapid.Generator[string] {
	line := rapid.StringMatching(`[ \t]{0,3}[a-z0-9{}\[\]":,#&*!|>%@'-]{0,6}[ \t]{0,2}`)

	return rapid.Custom(func(t *rapid.T) string {
		lines := rapid.SliceOfN(line, 2, 5).Draw(t, "lines")
		end := rapid.SampledFrom([]string{"", "\n", "\n\n"}).Draw(t, "end")

		return strings.Join(lines, "\n") + end
	})
}

// genFraction draws a finite float64 that the data model keeps as float64:
// not whole, or whole but beyond int64.
func genFraction() *rapid.Generator[float64] {
	return rapid.Float64().Filter(func(f float64) bool {
		if math.IsInf(f, 0) || math.IsNaN(f) {
			// JSON has no Inf or NaN. TestDecode_DataModel pins YAML's .inf instead.
			return false
		}

		return f != math.Trunc(f) || f < -(1<<63) || f >= 1<<63
	})
}

// genValue draws a value of the data model decode produces, nested up to depth.
func genValue(depth int) *rapid.Generator[any] {
	gens := []*rapid.Generator[any]{
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(genText(), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(genFraction(), func(f float64) any { return f }),
	}

	if depth > 0 {
		gens = append(gens,
			rapid.Map(rapid.SliceOfN(genValue(depth-1), 0, 4), func(s []any) any { return s }),
			rapid.Map(rapid.MapOfN(genText(), genValue(depth-1), 0, 4), func(m map[string]any) any { return m }),
		)
	}

	return rapid.OneOf(gens...)
}

// requireDataModel fails unless v holds only the types of the data model.
func requireDataModel(t require.TestingT, v any) {
	switch x := v.(type) {
	case nil, bool, string, int64, float64:
	case []any:
		for _, e := range x {
			requireDataModel(t, e)
		}
	case map[string]any:
		for _, e := range x {
			requireDataModel(t, e)
		}
	default:
		require.Failf(t, "not in the data model", "%T: %v", v, v)
	}
}

// encodeAs writes v with Encode, failing the test on errors.
func encodeAs(t rapid.TB, v any, format string) []byte {
	t.Helper()

	b, err := Encode(v, format)
	if err != nil {
		t.Fatalf("encoding %v as %s: %v", v, format, err)
	}

	return b
}

// decodeAs decodes data with the extension of the format, failing the test on errors.
func decodeAs(t rapid.TB, data []byte, ext string) any {
	t.Helper()

	doc, err := decode(data, ext)
	if err != nil {
		t.Fatalf("decoding %s %q: %v", ext, data, err)
	}

	return doc
}
