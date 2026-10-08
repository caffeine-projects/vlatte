package e2e

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/mailgun/raymond/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

var _ vlatte.TemplateEngine = (*handlebarsEngine)(nil)

// handlebarsEngine compiles Handlebars templates with github.com/mailgun/raymond. It is written as a user would
// write one: vlatte knows nothing of Handlebars, only the vlatte.TemplateEngine and vlatte.Template interfaces.
type handlebarsEngine struct {
	helpers map[string]any
}

// Compile parses text, and gives the template the engine's helpers.
func (e *handlebarsEngine) Compile(name, text string) (vlatte.Template, error) {
	tpl, err := raymond.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("handlebars: %s: %w", name, err)
	}

	tpl.RegisterHelpers(e.helpers)

	return &handlebarsTemplate{tpl: tpl}, nil
}

// handlebarsTemplate is a compiled Handlebars template. raymond renders to a string, which Execute writes at once.
type handlebarsTemplate struct {
	tpl *raymond.Template
}

func (t *handlebarsTemplate) Execute(w io.Writer, data any) error {
	out, err := t.tpl.Exec(data)
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, out)

	return err
}

// newHandlebars returns a Handlebars engine with the helper shout.
func newHandlebars() *handlebarsEngine {
	return &handlebarsEngine{helpers: map[string]any{"shout": strings.ToUpper}}
}

func TestTemplateEngine_HandlebarsAtEverySite(t *testing.T) {
	// The design check: a third-party engine compiles and renders every template of a server, from Go and from mock
	// files, with its own syntax and helpers. A mock file holds raw Handlebars, which Go templates can't parse, so
	// files are not run through Go templates.
	checkTemplateSites(t, newHandlebars(), "{{shout Request.Method}}-{{Request.URLPathSegments.[1]}}", "PUT-42")
}

func TestTemplateEngine_HandlebarsReadsTheTemplateData(t *testing.T) {
	// Every engine reads the same data: the request, the server, the server's data, and the reply's template data.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(newHandlebars()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.SetData(map[string]any{"greeting": "hi"})
	m.MustMock(mockhttp.Getf("/data").Reply(mockhttp.OK().
		SetTemplateData(map[string]any{"name": "ana"}).
		BodyTemplate("{{Request.ID}} {{{Server.URL}}} {{Data.greeting}} {{Extra.name}}")))

	res, body := handlebarsGet(t, m.URL("/data"))
	require.Equal(t, http.StatusOK, res.StatusCode, body)

	fields := strings.Fields(body)
	require.Len(t, fields, 4, body)

	_, err := uuid.Parse(fields[0])
	require.NoError(t, err, "Request.ID")
	require.Equal(t, []string{m.URL(), "hi", "ana"}, fields[1:])
}

func TestTemplateEngine_HandlebarsEscapingIsTheEngines(t *testing.T) {
	// vlatte writes what the engine renders: Handlebars escapes HTML in {{ }} and not in {{{ }}}.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(newHandlebars()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/html").Reply(mockhttp.OK().
		SetTemplateData(map[string]any{"html": "<b>"}).
		BodyTemplate("{{Extra.html}} {{{Extra.html}}}")))

	_, body := handlebarsGet(t, m.URL("/html"))
	require.Equal(t, "&lt;b&gt; <b>", body)
}

func TestTemplateEngine_HandlebarsSyntaxErrorsFailTheMock(t *testing.T) {
	// The engine's compile errors fail the mock when it is built, whatever defines the template.
	wh, err := webhook.New()
	require.NoError(t, err)

	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(newHandlebars()).OnCompleteListener("webhook", wh))
	t.Cleanup(m.Close)

	const broken = "{{#if Request.Method}}open"

	_, err = m.Mock(mockhttp.Getf("/go").Reply(mockhttp.OK().BodyTemplate(broken)))
	require.ErrorContains(t, err, "reply: handlebars: body: ")

	_, err = m.Mock(mockhttp.FromBytes([]byte(`{"request": {"path": "/file"}, "response": {"body": "`+broken+`", "template": {"enabled": true}}}`), "json"))
	require.ErrorContains(t, err, "reply: handlebars: body: ")

	_, err = m.Mock(mockhttp.Getf("/hook").
		OnCompleteEvent(vlatte.EventConfig{Name: "webhook", Parameters: map[string]any{"url": "http://localhost", "body_template": broken}}).
		Reply(mockhttp.NoContent()))
	require.ErrorContains(t, err, "mock: on_complete listener webhook at index 0: ")
	require.ErrorIs(t, err, webhook.ErrRenderingTemplate)
}

func TestTemplateEngine_HandlebarsUnderConcurrentRequests(t *testing.T) {
	// A mock renders its templates for every request at once, so the engine's templates run concurrently.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().TemplateEngine(newHandlebars()))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/echo/*").Reply(mockhttp.OK().BodyTemplate("{{shout Request.Method}} {{Request.URLPathSegments.[1]}}")))

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			res, err := http.Get(m.URL("/echo/" + strconv.Itoa(i)))
			if !assert.NoError(t, err) {
				return
			}

			b, err := io.ReadAll(res.Body)
			assert.NoError(t, err)
			assert.NoError(t, res.Body.Close())
			assert.Equal(t, "GET "+strconv.Itoa(i), string(b))
		})
	}

	wg.Wait()
}

// handlebarsGet sends a GET and reads the whole response.
func handlebarsGet(t *testing.T, url string) (*http.Response, string) {
	t.Helper()

	res, err := http.Get(url)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, string(b)
}
