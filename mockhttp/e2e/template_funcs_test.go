package e2e

import (
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte"
)

func TestTemplateEngine_GoFunctionsAtEverySite(t *testing.T) {
	// The functions of the server's Go engine reach every template: they used to be stored and never used.
	engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{"shout": strings.ToUpper}))

	checkTemplateSites(t, engine, "{{ shout .Request.Method }}-{{ index .Request.URLPathSegments 1 }}", "PUT-42")
}
