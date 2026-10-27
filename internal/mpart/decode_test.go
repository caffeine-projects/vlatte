package mpart

import (
	json "encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/value"
)

func TestDecode_Rules(t *testing.T) {
	// One case per rule of the multipart object. Each names the problem's code and key, so the user can find it, and
	// the words that say what to do.
	type want struct {
		code    string
		key     string
		message string
		hint    string
	}

	tests := []struct {
		name string
		doc  string
		want []want
	}{
		{"not an object", `[]`, []want{{config.CodeInvalidValue, "m", "must be an object, not a list", ""}}},
		{"no type", `{"parts": [{}]}`, []want{{config.CodeMissingValue, "m.type", "m.type is required", "name the subtype, such as form-data or mixed"}}},
		{"a whole media type", `{"type": "multipart/mixed", "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.type", `must be a subtype, not "multipart/mixed"`, "write the subtype only"}}},
		{"type that isn't a token", `{"type": "a b", "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.type", `must be a token, such as mixed, not "a b"`, ""}}},
		{"type as a number", `{"type": 1, "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.type", "m.type must be a string, not a number", `write it in quotes: "1"`}}},
		{"empty boundary", `{"type": "mixed", "boundary": "", "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.boundary", "m.boundary is empty", "leave it out for a random boundary"}}},
		{"boundary ending in a space", `{"type": "mixed", "boundary": "a ", "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.boundary", "must be 1 to 70 letters, digits or", ""}}},
		{"boundary too long", `{"type": "mixed", "boundary": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.boundary", "must be 1 to 70", ""}}},
		{"boundary in params", `{"type": "mixed", "params": {"Boundary": "x"}, "parts": [{}]}`, []want{{config.CodeNotAllowed, "m.params.Boundary", "m.params.Boundary can't be set", "set boundary instead"}}},
		{"param that isn't text", `{"type": "mixed", "params": {"start": 1}, "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.params.start", "must be a string, not a number", ""}}},
		{"param name that isn't a token", `{"type": "mixed", "params": {"a b": "x"}, "parts": [{}]}`, []want{{config.CodeInvalidValue, "m.params.a b", "is not a parameter of a media type", ""}}},
		{"no part", `{"type": "mixed", "parts": []}`, []want{{config.CodeInvalidValue, "m.parts", "m.parts is empty", "add at least one part"}}},
		{"no parts key", `{"type": "mixed"}`, []want{{config.CodeMissingValue, "m.parts", "m.parts is required", "add at least one part"}}},
		{"parts as an object", `{"type": "mixed", "parts": {}}`, []want{{config.CodeInvalidValue, "m.parts", "must be a list, not an object", ""}}},
		{"part as text", `{"type": "mixed", "parts": ["x"]}`, []want{{config.CodeInvalidValue, "m.parts[0]", "must be an object, not a string", ""}}},
		{"part key with a typo", `{"type": "mixed", "parts": [{"nmae": "x"}]}`, []want{{config.CodeUnknownKey, "m.parts[0].nmae", "not a known setting", "did you mean m.parts[0].name?"}}},
		{"form-data part without a name", `{"type": "form-data", "parts": [{"name": "a"}, {"body": "x"}]}`, []want{{config.CodeMissingValue, "m.parts[1].name", "m.parts[1].name is required in a form-data part", "name the form field"}}},
		{"form-data in capitals, a part without a name", `{"type": "FORM-DATA", "parts": [{"body": "x"}]}`, []want{{config.CodeMissingValue, "m.parts[0].name", "is required in a form-data part", "name the form field"}}},
		{"a line of a body that starts with the boundary", `{"type": "mixed", "boundary": "b", "parts": [{"body": "a"}, {"body": "a\n--b c"}]}`, []want{{config.CodeInvalidValue, "m.parts[1].body", `m.parts[1].body has a line that starts with the boundary "b"`, "choose another boundary"}}},
		{"name as a number", `{"type": "form-data", "parts": [{"name": 1}]}`, []want{{config.CodeInvalidValue, "m.parts[0].name", "must be a string, not a number", ""}}},
		{"null body", `{"type": "mixed", "parts": [{"body": null}]}`, []want{{config.CodeInvalidValue, "m.parts[0].body", "m.parts[0].body is null", `write "" for an empty body`}}},
		{"body and body_file", `{"type": "mixed", "parts": [{"body": "x", "body_file": "x.bin"}]}`, []want{{config.CodeConflict, "m.parts[0].body_file", "m.parts[0].body_file is set with m.parts[0].body", "keep one of them"}}},
		{"empty body_file", `{"type": "mixed", "parts": [{"body_file": ""}]}`, []want{{config.CodeInvalidValue, "m.parts[0].body_file", "expected a file name", ""}}},
		{"body and body_file, and a typo", `{"type": "mixed", "parts": [{"body": "x", "body_file": "x.bin", "nmae": "a"}]}`, []want{
			{config.CodeConflict, "m.parts[0].body_file", "m.parts[0].body_file is set with m.parts[0].body", "keep one of them"},
			{config.CodeUnknownKey, "m.parts[0].nmae", "not a known setting", "did you mean m.parts[0].name?"},
		}},
		{"negative delay", `{"type": "mixed", "parts": [{"delay": -5}]}`, []want{{config.CodeInvalidValue, "m.parts[0].delay", "must be 0 or more", ""}}},
		{"delay as text", `{"type": "mixed", "parts": [{"delay": "soon"}]}`, []want{{config.CodeInvalidValue, "m.parts[0].delay", "milliseconds or a Go duration", ""}}},
		{"header as a number", `{"type": "mixed", "parts": [{"header": {"X-A": 1}}]}`, []want{{config.CodeInvalidValue, "m.parts[0].header.X-A", "must be a string or a list of strings", `write it in quotes: "1"`}}},
		{"header list with a number", `{"type": "mixed", "parts": [{"header": {"X-A": ["a", 1]}}]}`, []want{{config.CodeInvalidValue, "m.parts[0].header.X-A[1]", "must be a string, not a number", ""}}},
		{"header name that isn't a token", `{"type": "mixed", "parts": [{"header": {"a b": "x"}}]}`, []want{{config.CodeInvalidValue, "m.parts[0].header.a b", "is not a header name", ""}}},
		{"header without a name", `{"type": "mixed", "parts": [{"header": {"": "x"}}]}`, []want{{config.CodeInvalidValue, "m.parts[0].header.", "is not a header name", ""}}},
		{"header with a line break", `{"type": "mixed", "parts": [{"header": {"X-A": "a\r\nX-B: b"}}]}`, []want{{config.CodeInvalidValue, "m.parts[0].header.X-A", "holds a line break", ""}}},
		{"content type with a line break", `{"type": "mixed", "parts": [{"content_type": "a/b\nX: y"}]}`, []want{{config.CodeInvalidValue, "m.parts[0].content_type", "holds a line break", ""}}},
		{"content_type and a Content-Type header", `{"type": "mixed", "parts": [{"content_type": "a/b", "header": {"content-type": "c/d"}}]}`, []want{{config.CodeConflict, "m.parts[0].header.content-type", "m.parts[0].header.content-type is set with m.parts[0].content_type", "keep one of them"}}},
		{"name and a Content-Disposition header", `{"type": "form-data", "parts": [{"name": "a", "header": {"Content-Disposition": "form-data"}}]}`, []want{{config.CodeConflict, "m.parts[0].header.Content-Disposition", "is set with m.parts[0].name", ""}}},
		{"filename and a Content-Disposition header", `{"type": "mixed", "parts": [{"filename": "a", "header": {"Content-Disposition": "inline"}}]}`, []want{{config.CodeConflict, "m.parts[0].header.Content-Disposition", "is set with m.parts[0].filename", ""}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := value.DecodeJSON([]byte(tc.doc))
			require.NoError(t, err)

			_, err = Decode(config.Value{Key: "m", Origin: "doc", Any: doc})

			cfgErr, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "want a *config.Error, got %v", err)
			require.Len(t, cfgErr.Problems, len(tc.want), "%v", cfgErr.Problems)

			for _, w := range tc.want {
				i := slices.IndexFunc(cfgErr.Problems, func(p config.Problem) bool { return p.Code == w.code && p.Key == w.key })
				require.GreaterOrEqual(t, i, 0, "no %s problem for %q in %v", w.code, w.key, cfgErr.Problems)

				p := cfgErr.Problems[i]
				require.Equal(t, "doc", p.Origin)
				require.Contains(t, p.Message, w.message)
				require.Contains(t, p.Hint, w.hint)
			}
		})
	}
}

func TestDecode_ALineBreakAroundAHeaderValue(t *testing.T) {
	// A YAML block scalar ends its value with a line break, which isn't written: only a line break inside is a problem.
	doc, err := value.DecodeJSON([]byte(`{"type": "mixed", "parts": [{"content_type": "a/b\n", "header": {"X-A": "{{ .V }}\n"}}]}`))
	require.NoError(t, err)

	_, err = Decode(config.Value{Key: "m", Origin: "doc", Any: doc})
	require.NoError(t, err)
}

func TestDecode_AValidObject(t *testing.T) {
	doc, err := value.DecodeJSON([]byte(`{
		"type": "related", "boundary": "b0", "params": {"type": "application/json", "start": "<root>"},
		"parts": [
			{"header": {"Content-Id": "<root>", "X-A": ["1", "2", "3"]}, "body": {"id": 1}},
			{"name": "report", "filename": "r.pdf", "content_type": "application/pdf", "body_file": "r.pdf", "delay": "500ms"},
			{"body": "text"}
		]}`))
	require.NoError(t, err)

	s, err := Decode(config.Value{Key: "m", Origin: "doc", Any: doc})
	require.NoError(t, err)

	require.Equal(t, &Spec{
		Subtype:  "related",
		Boundary: "b0",
		Params:   map[string]string{"type": "application/json", "start": "<root>"},
		Parts: []*PartSpec{
			{Header: []Header{{Name: "Content-Id", Value: "<root>"}, {Name: "X-A", Value: "1"}, {Name: "X-A", Value: "2"}, {Name: "X-A", Value: "3"}}, Body: []byte(`{"id":1}`), Kind: BodyJSON},
			{Name: "report", Filename: "r.pdf", ContentType: "application/pdf", File: "r.pdf", Delay: 500_000_000},
			{Body: []byte("text"), Kind: BodyText},
		},
	}, s)

	// Doc writes back what Decode read, so admin GET output loads again.
	require.Equal(t, doc, s.Doc())
}

func TestPropDecode_ReadsWhatDocWrites(t *testing.T) {
	// MP5: Decode reads a spec's Doc back as the same spec: the same Doc, and the same bytes on the wire. A byte
	// body comes back as text, which writes the same bytes.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		s.Boundary = genBoundary().Draw(t, "boundary")

		for _, p := range s.Parts {
			// A mock file is text, so JSON and YAML can't hold such a body: it goes through Go only.
			if !utf8.Valid(p.Body) {
				p.Body = []byte("text")
			}
		}

		// A mock file is JSON or YAML, so the document goes through JSON as a file does.
		data, err := json.Marshal(s.Doc(), json.Deterministic(true))
		require.NoError(t, err)

		read, err := value.DecodeJSON(data)
		require.NoError(t, err)

		got, err := Decode(config.Value{Key: "multipart", Origin: "doc", Any: read})
		if slices.ContainsFunc(s.Parts, func(p *PartSpec) bool { return p.File == "" && lineStarts(p.Body, s.Boundary) }) {
			require.ErrorContains(t, err, "has a line that starts with the boundary", "MP2")
			return
		}

		require.NoError(t, err)

		got.Dir = s.Dir
		require.Equal(t, s.Doc(), got.Doc())

		want, err := Compile(s, _engine)
		require.NoError(t, err)

		decoded, err := Compile(got, _engine)
		require.NoError(t, err)

		wantBody, wantPauses, wantErr := want.Encode(nil)
		gotBody, gotPauses, gotErr := decoded.Encode(nil)

		require.Equal(t, wantErr, gotErr)
		require.Equal(t, string(wantBody), string(gotBody))
		require.Equal(t, wantPauses, gotPauses)
		require.Equal(t, want.ContentType(), decoded.ContentType())
	})
}

// specFault is an invalid setting injected into a valid multipart document, and the problem it must cause.
type specFault struct {
	area   string // what it touches, so faults drawn together never touch the same thing
	code   string
	key    string
	inject func(doc map[string]any)
}

// _specFaults touch distinct settings of a valid multipart document. Each document has one part at least.
var _specFaults = []specFault{
	{"type", config.CodeMissingValue, "m.type", func(doc map[string]any) { delete(doc, "type") }},
	{"type", config.CodeInvalidValue, "m.type", func(doc map[string]any) { doc["type"] = "multipart/mixed" }},
	{"boundary", config.CodeInvalidValue, "m.boundary", func(doc map[string]any) { doc["boundary"] = "ends in a space " }},
	{"params", config.CodeNotAllowed, "m.params.boundary", func(doc map[string]any) { doc["params"] = map[string]any{"boundary": "x"} }},
	{"parts", config.CodeInvalidValue, "m.parts", func(doc map[string]any) { doc["parts"] = []any{} }},
	{"parts", config.CodeConflict, "m.parts[0].header.Content-Type", func(doc map[string]any) {
		p := doc["parts"].([]any)[0].(map[string]any)
		p["content_type"] = "a/b"
		p["header"] = map[string]any{"Content-Type": "c/d"}
	}},
	{"parts", config.CodeInvalidValue, "m.parts[0].header.X-N", func(doc map[string]any) {
		doc["parts"].([]any)[0].(map[string]any)["header"] = map[string]any{"X-N": "a\nb"}
	}},
	{"parts", config.CodeInvalidValue, "m.parts[0].delay", func(doc map[string]any) { doc["parts"].([]any)[0].(map[string]any)["delay"] = int64(-5) }},
	{"parts", config.CodeConflict, "m.parts[0].body_file", func(doc map[string]any) {
		p := doc["parts"].([]any)[0].(map[string]any)
		p["body"] = "x"
		p["body_file"] = "x.bin"
	}},
	{"parts", config.CodeUnknownKey, "m.parts[0].nmae", func(doc map[string]any) { doc["parts"].([]any)[0].(map[string]any)["nmae"] = "x" }},
}

func TestPropDecode_EveryFaultOnce(t *testing.T) {
	// MP5: each fault of a multipart document is reported once, with its code and key, and nothing else is: the
	// checks of the model don't repeat what decoding already found.
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		s := genSpec(dir).Draw(t, "spec")
		for _, p := range s.Parts {
			p.Body = []byte("text")
		}

		doc := s.Doc()

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_specFaults), 1, len(_specFaults),
			func(f specFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(doc)
			want = append(want, f.code+" "+f.key)
		}

		_, err := Decode(config.Value{Key: "m", Origin: "doc", Any: doc})

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)

		got := make([]string, 0, len(cfgErr.Problems))
		for _, p := range cfgErr.Problems {
			require.Equal(t, "doc", p.Origin)
			got = append(got, p.Code+" "+p.Key)
		}

		require.ElementsMatch(t, want, got)
	})
}

func FuzzMultipartDecode(f *testing.F) {
	// MP6: no document panics Decode, and every problem is keyed under the object's key.
	for _, seed := range []string{
		`{"type": "mixed", "parts": [{"body": "x"}]}`,
		`{"type": "form-data", "boundary": "b", "params": {"start": "<a>"}, "parts": [{"name": "a", "filename": "a.png", "body_file": "a.png", "delay": "1s"}]}`,
		`{"type": "related", "parts": [{"header": {"content-id": ["<a>", "<b>"]}, "body": {"id": 1}}]}`,
		`{"type": 1, "parts": {}}`,
		`{"parts": [null, 1, "x", {"body": null}]}`,
		`[]`,
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := value.DecodeJSON(data)
		if err != nil {
			return
		}

		_, err = Decode(config.Value{Key: "m", Origin: "fuzz", Any: doc})
		if err == nil {
			return
		}

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)

		for _, p := range cfgErr.Problems {
			require.True(t, p.Key == "m" || strings.HasPrefix(p.Key, "m.") || strings.HasPrefix(p.Key, "m["), "key %q", p.Key)
			require.NotEmpty(t, p.Code)
		}
	})
}
