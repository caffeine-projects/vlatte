package mpart

import (
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"pgregory.net/rapid"
)

// _genSubtypes are the subtypes specs draw, besides random tokens.
var _genSubtypes = []string{"form-data", "mixed", "related", "alternative", "x-mixed-replace"}

// genBoundary draws a boundary that Writer.SetBoundary takes: 1 to 70 of its characters, not ending in a space.
func genBoundary() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9A-Za-z'()+_,\-./:=?]([0-9A-Za-z'()+_,\-./:=? ]{0,68}[0-9A-Za-z'()+_,\-./:=?])?`)
}

// genText draws text for names and filenames: unicode, and the characters a Content-Disposition quotes or escapes,
// CR and LF included.
func genText() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 ._\-"\\;=/éü\r\n]{1,10}`)
}

// genHeaderValue draws a header value: no CR or LF, which Check rejects, and no space at either end, which readers
// trim (TestEncode_HeaderValuesLoseOuterSpaces).
func genHeaderValue() *rapid.Generator[string] {
	return rapid.StringMatching(`([A-Za-z0-9._\-"\\;=/:<>@éü]([A-Za-z0-9 ._\-"\\;=/:<>@éü]{0,8}[A-Za-z0-9._\-"\\;=/:<>@éü])?)?`)
}

// genBytes draws a body with line breaks and dashes, as delimiter lines have.
func genBytes() *rapid.Generator[[]byte] {
	return rapid.Map(rapid.SliceOfN(rapid.SampledFrom([]string{"\r\n", "--", "\r\n--", "\x00", "\xff", "a", "{{ .X }}"}), 0, 12),
		func(chunks []string) []byte {
			var b []byte
			for _, c := range chunks {
				b = append(b, c...)
			}

			return b
		})
}

// genJSON draws a value of the data model, for JSON bodies.
func genJSON() *rapid.Generator[any] {
	return rapid.OneOf(
		rapid.Map(rapid.String(), func(s string) any { return s }),
		rapid.Map(rapid.Int64(), func(n int64) any { return n }),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Just[any](nil),
		rapid.Map(rapid.SliceOfN(rapid.Int64(), 0, 3), func(l []int64) any { return toAny(l) }),
		rapid.Map(rapid.MapOfN(rapid.StringMatching(`[a-z]{1,4}`), rapid.String(), 0, 3), func(m map[string]string) any {
			out := make(map[string]any, len(m))
			for k, v := range m {
				out[k] = v
			}

			return out
		}),
	)
}

func toAny[E any](s []E) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}

	return out
}

// genPart draws a part that checks clean. Body files go to dir.
func genPart(formData bool, dir string, i int) *rapid.Generator[*PartSpec] {
	return rapid.Custom(func(t *rapid.T) *PartSpec {
		p := new(PartSpec)
		if formData || rapid.Bool().Draw(t, "has name") {
			p.Name = genText().Draw(t, "name")
		}

		if rapid.Bool().Draw(t, "has filename") {
			p.Filename = genText().Draw(t, "filename")
		}

		if rapid.Bool().Draw(t, "has content type") {
			p.ContentType = rapid.SampledFrom([]string{"text/plain", "image/png; x=1", "application/vnd.a+json"}).Draw(t, "content type")
		}

		for _, name := range rapid.SliceOfNDistinct(rapid.SampledFrom([]string{"X-A", "x-b", "Content-Id"}), 0, 2,
			func(s string) string { return s }).Draw(t, "header names") {
			for range rapid.IntRange(1, 2).Draw(t, "header values") {
				p.Header = append(p.Header, Header{Name: name, Value: genHeaderValue().Draw(t, "header value")})
			}
		}

		// A Content-Type header stands in for content_type only when there is none: Check rejects both.
		if p.ContentType == "" && rapid.Bool().Draw(t, "content type header") {
			p.Header = append(p.Header, Header{Name: "content-type", Value: "text/csv"})
		}

		switch rapid.IntRange(0, 3).Draw(t, "body kind") {
		case 0:
			p.Body, p.Kind = genBytes().Draw(t, "bytes"), BodyBytes
		case 1:
			p.Body, p.Kind = []byte(rapid.String().Draw(t, "text")), BodyText
		case 2:
			b, err := json.Marshal(genJSON().Draw(t, "json"), json.Deterministic(true))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			p.Body, p.Kind = b, BodyJSON
		default:
			p.File = fmt.Sprintf("part%d.bin", i)
			if err := os.WriteFile(filepath.Join(dir, p.File), genBytes().Draw(t, "file content"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}

		if rapid.Bool().Draw(t, "has delay") {
			p.Delay = time.Duration(rapid.Int64Range(1, int64(time.Hour)).Draw(t, "delay"))
		}

		return p
	})
}

// genSpec draws a spec that checks clean, with its body files in dir.
func genSpec(dir string) *rapid.Generator[*Spec] {
	return rapid.Custom(func(t *rapid.T) *Spec {
		s := &Spec{Dir: dir}

		s.Subtype = rapid.OneOf(rapid.SampledFrom(_genSubtypes), rapid.StringMatching(`[a-z][a-z0-9.+-]{0,8}`)).Draw(t, "subtype")
		if rapid.Bool().Draw(t, "has boundary") {
			s.Boundary = genBoundary().Draw(t, "boundary")
		}

		if rapid.Bool().Draw(t, "has params") {
			s.Params = map[string]string{
				"type":  rapid.SampledFrom([]string{"application/json", "text/xml"}).Draw(t, "type"),
				"start": "<" + rapid.StringMatching(`[a-z]{1,6}`).Draw(t, "start") + ">",
			}
		}

		for i := range rapid.IntRange(1, 8).Draw(t, "parts") {
			s.Parts = append(s.Parts, genPart(s.Subtype == "form-data", dir, i).Draw(t, "part"))
		}

		return s
	})
}
