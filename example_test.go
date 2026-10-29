package vlatte_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

// exampleT reports what fails on the standard output, as a test's *testing.T reports it in the test's log.
type exampleT struct{}

func (exampleT) Helper()                        {}
func (exampleT) Logf(format string, a ...any)   { fmt.Printf(format+"\n", a...) }
func (exampleT) Error(a ...any)                 { fmt.Println(a...) }
func (exampleT) Errorf(format string, a ...any) { fmt.Printf(format+"\n", a...) }

// get sends a GET of path to m, with header, and returns the status and body of the answer.
func get(m *mockhttp.Server, path string, header http.Header) (int, string) {
	req, err := http.NewRequest(http.MethodGet, m.URL(path), nil)
	if err != nil {
		panic(err)
	}

	req.Header = header

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		panic(err)
	}

	return res.StatusCode, string(body)
}

func ExampleMatcherFunc() {
	// A matcher of your own, for a check package match doesn't have: an even number of characters.
	even := vlatte.MatcherFunc("even", func(arg vlatte.MatcherArg) (bool, error) {
		s, ok := arg.Value().(string)
		return ok && len(s)%2 == 0, nil
	})

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/pairs").Header("X-Code", even).Reply(mockhttp.OK()))

	matched, _ := get(m, "/pairs", http.Header{"X-Code": {"ab"}})
	unmatched, _ := get(m, "/pairs", http.Header{"X-Code": {"abc"}})

	fmt.Println(matched, unmatched)
	// Output: 200 418
}

func ExampleStepFunc() {
	// A step sees the bytes of the answer on their way to the client: this one shouts them.
	shout := vlatte.StepFunc(func(_ context.Context, b []byte, next vlatte.Stage) (int, error) {
		if _, err := next.Write(bytes.ToUpper(b)); err != nil {
			return 0, err
		}

		return len(b), nil
	})

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	m.MustMock(mockhttp.Getf("/hello").Step(shout).Reply(mockhttp.OK().PlainText("hello")))

	fmt.Println(get(m, "/hello", nil))
	// Output: 200 HELLO
}

func ExampleTimes() {
	t := exampleT{} // a test's *testing.T

	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	defer m.Close()

	// The scope MustMock returns is an Inspector of its mocks; the server is one of all its mocks.
	scope := m.MustMock(mockhttp.Getf("/hello").Reply(mockhttp.OK()))

	get(m, "/hello", nil)
	get(m, "/hello", nil)

	fmt.Println(vlatte.Times(t, scope, 2))
	// Output: true
}

func ExampleNewGoTemplateEngine() {
	engine := vlatte.MustNewGoTemplateEngine(vlatte.WithTemplateFuncs(map[string]any{
		"shout": strings.ToUpper,
	}))

	tmpl, err := engine.Compile("greeting", `{{ shout .Name }}!`)
	if err != nil {
		panic(err)
	}

	if err := tmpl.Execute(os.Stdout, map[string]any{"Name": "ada"}); err != nil {
		panic(err)
	}
	// Output: ADA!
}

func ExampleNewGoTemplateEngine_pkce() {
	// The S256 code challenge of a PKCE code verifier (RFC 7636): base64url, without padding, of its SHA-256.
	engine := vlatte.MustNewGoTemplateEngine()

	tmpl, err := engine.Compile("challenge", `{{ hash "sha256" "base64url" .Verifier }}`)
	if err != nil {
		panic(err)
	}

	if err := tmpl.Execute(os.Stdout, map[string]any{"Verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}); err != nil {
		panic(err)
	}
	// Output: E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM
}
