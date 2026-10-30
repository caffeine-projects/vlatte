package test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestREADME_QuickStartsMatchRunnableExamples(t *testing.T) {
	// Mocha #53: examples and the README drifted from the API. Each README block must be a tested Example.
	t.Chdir("..")
	content, err := os.ReadFile("README.md")
	require.NoError(t, err)
	readme := strings.ReplaceAll(string(content), "\r\n", "\n")

	tests := []struct {
		section string
		source  string
	}{
		{section: "### HTTP in a test", source: "mockhttp/example_test.go"},
		{section: "### gRPC in a test", source: "mockgrpc/example_test.go"},
	}

	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			_, section, ok := strings.Cut(readme, "\n"+tt.section+"\n")
			require.True(t, ok, "README.md has no section %q", tt.section)
			section, _, _ = strings.Cut(section, "\n### ")
			_, block, ok := strings.Cut(section, "```go\n")
			require.True(t, ok, "README.md has no Go block in %q", tt.section)
			block, _, ok = strings.Cut(block, "\n```")
			require.True(t, ok, "README.md has no closing Go fence in %q", tt.section)

			content, err := os.ReadFile(tt.source)
			require.NoError(t, err)
			source := strings.ReplaceAll(string(content), "\r\n", "\n")
			_, example, ok := strings.Cut(source, "// #region example\n")
			require.True(t, ok, "%s has no example region", tt.source)
			example, _, ok = strings.Cut(example, "// #endregion example")
			require.True(t, ok, "%s has no closing example region", tt.source)
			example = strings.TrimSpace(example)
			require.NotEmpty(t, example, "%s has an empty example region", tt.source)
			require.Equal(t, example, block, "copy the example region from %s into README.md", tt.source)
		})
	}
}
