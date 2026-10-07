package mpart

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncode_HeaderValuesLoseOuterSpaces(t *testing.T) {
	// genHeaderValue draws no space at either end of a value: a reader trims them, as HTTP header values are.
	e, err := Compile(&Spec{Subtype: "mixed", Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: " a "}}}}})
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)

	_, params, err := mime.ParseMediaType(e.ContentType())
	require.NoError(t, err)

	part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextRawPart()
	require.NoError(t, err)
	require.Equal(t, "a", part.Header.Get("X-A"))

	_, err = io.ReadAll(part)
	require.NoError(t, err)
}

func TestEncode_TemplatedBodyFileName(t *testing.T) {
	// A templated spec renders the name of a body file, not its content: a file may be binary.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("{{ .Name }} as is"), 0o600))

	e, err := Compile(&Spec{Subtype: "mixed", Templated: true, Dir: dir, Parts: []*PartSpec{{File: "{{ .Name }}.txt"}}})
	require.NoError(t, err)

	body, _, err := e.Encode(map[string]any{"Name": "a"})
	require.NoError(t, err)
	require.Contains(t, string(body), "\r\n\r\n{{ .Name }} as is\r\n")
}

func TestEncode_Errors(t *testing.T) {
	// What fails at Encode, rendering with the data of one use, names the part.
	tests := []struct {
		name string
		spec *Spec
		data any
		want string
	}{
		{"a body file that doesn't exist", &Spec{Subtype: "mixed", Parts: []*PartSpec{{Body: []byte("a")}, {File: "no-such-file"}}}, nil, "multipart: parts[1]: open no-such-file: "},
		{"a header that renders a line break", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: "{{ .V }}"}}}}}, map[string]any{"V": "a\nX-B: b"}, "multipart: parts[0]: header.X-A holds a line break"},
		{"a content type that renders a line break", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{ContentType: "{{ .V }}"}}}, map[string]any{"V": "a/b\r\nX: y"}, "multipart: parts[0]: content_type holds a line break"},
		{"a form-data name that renders empty", &Spec{Subtype: "form-data", Templated: true, Parts: []*PartSpec{{Name: "{{ .V }}"}}}, map[string]any{"V": ""}, "multipart: parts[0]: name is empty: a form-data part needs one"},
		{"a template that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Body: []byte("{{ .V.W }}"), Kind: BodyText}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: body:1:5: executing"},
		{"a name that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Name: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: name:1:5: executing"},
		{"a filename that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Filename: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: filename:1:5: executing"},
		{"a content type that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{ContentType: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: content_type:1:5: executing"},
		{"a header that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Header: []Header{{Name: "X-A", Value: "{{ .V.W }}"}}}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: header.X-A:1:5: executing"},
		{"a body file name that fails", &Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{File: "{{ .V.W }}"}}}, map[string]any{"V": 1}, "multipart: parts[0]: template: body_file:1:5: executing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, err := Compile(tc.spec)
			require.NoError(t, err)

			_, _, err = e.Encode(tc.data)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCompile_Errors(t *testing.T) {
	// Compile reports every issue of the spec, and template syntax errors, naming where they are.
	_, err := Compile(&Spec{Subtype: "form-data", Boundary: "a ", Parts: []*PartSpec{{Err: errors.New("json: cannot marshal")}}})
	require.EqualError(t, err, "multipart: boundary must be 1 to 70 letters, digits or '()+_,-./:=? characters, not ending in a space, not \"a \"; "+
		"parts[0].body json: cannot marshal; parts[0].name is required in a form-data part; name the form field")

	_, err = Compile(&Spec{Subtype: "mixed", Templated: true, Parts: []*PartSpec{{Filename: "{{ .A"}}})
	require.ErrorContains(t, err, "multipart: parts[0]: filename: template: filename:1: ")
}

func TestEncode_ContentTypeParameters(t *testing.T) {
	// The Content-Type holds the boundary and the parameters, quoted where they need it.
	e, err := Compile(&Spec{
		Subtype: "related", Boundary: "b0",
		Params: map[string]string{"type": "application/xop+xml", "start": "<root>", "start-info": "text/xml"},
		Parts:  []*PartSpec{{Body: []byte("x")}},
	})
	require.NoError(t, err)
	require.Equal(t, `multipart/related; boundary=b0; start="<root>"; start-info="text/xml"; type="application/xop+xml"`, e.ContentType())
}

func TestEncode_DispositionOutsideForms(t *testing.T) {
	// Outside form-data, a name or a filename makes an attachment; a part with neither has no disposition.
	e, err := Compile(&Spec{Subtype: "mixed", Boundary: "b", Parts: []*PartSpec{{Name: "n"}, {Filename: `a"b.txt`}, {}}})
	require.NoError(t, err)

	body, _, err := e.Encode(nil)
	require.NoError(t, err)
	require.Equal(t, "--b\r\nContent-Disposition: attachment; name=\"n\"\r\n\r\n"+
		"\r\n--b\r\nContent-Disposition: attachment; filename=\"a\\\"b.txt\"\r\nContent-Type: application/octet-stream\r\n\r\n"+
		"\r\n--b\r\n\r\n"+
		"\r\n--b--\r\n", string(body))
}
