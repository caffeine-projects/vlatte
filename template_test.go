package vlatte

import (
	"bytes"
	"testing"
	texttemplate "text/template"

	"github.com/stretchr/testify/require"
)

func TestTextTemplate(t *testing.T) {
	cases := []struct {
		name     string
		template string
		data     any
		expected string
	}{
		{
			name:     "Basic Hello World",
			template: "Hello {{.}}",
			data:     "World",
			expected: "Hello World",
		},
		{
			name:     "Simple Map",
			template: "URL: {{.URL}}",
			data:     map[string]string{"URL": "http://example.com"},
			expected: "URL: http://example.com",
		},
		{
			name:     "Only Text",
			template: "Just some text",
			data:     map[string]string{"unused": "value"},
			expected: "Just some text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdTpl := texttemplate.Must(texttemplate.New("test").Parse(tc.template))
			tmpl := TemplateText(stdTpl)

			buf := &bytes.Buffer{}

			err := tmpl.Execute(buf, tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, buf.String())
		})
	}
}
