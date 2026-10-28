package mockhttp

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"pgregory.net/rapid"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/internal/mpart"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
)

// decodeDoc builds a YAML mock document for app, as the admin API does.
func decodeDoc(app *Server, doc string) (*Mock, error) {
	return mockDoc{origin: "doc", format: "yaml", data: []byte(doc)}.build(app)
}

// mockProblems returns the problems of a mock document's error, failing unless it is a mock *config.Error.
func mockProblems(t require.TestingT, err error) []config.Problem {
	cfgErr, ok := errors.AsType[*config.Error](err)
	require.True(t, ok, "want a *config.Error, got %v", err)
	require.Equal(t, "mock", cfgErr.Scope)

	return cfgErr.Problems
}

// want is the part of a problem a rule fixes: its code, its key, and words its message and hint must hold.
type want struct {
	code    string
	key     string
	message string
	hint    string
}

func requireProblem(t *testing.T, problems []config.Problem, w want) {
	t.Helper()

	i := slices.IndexFunc(problems, func(p config.Problem) bool { return p.Code == w.code && p.Key == w.key })
	require.GreaterOrEqual(t, i, 0, "no %s problem for %q in %v", w.code, w.key, problems)

	p := problems[i]
	require.Equal(t, "doc", p.Origin)
	require.Contains(t, p.Message, w.message)
	require.Contains(t, p.Hint, w.hint)
}

func TestMockSchema_Rules(t *testing.T) {
	// One case per rule of the mock file format. Each names the problem's code and key, so the user can find it,
	// and the words that say what to do.
	m := MustNew(Setup().Silent())

	const ok = "request: {path: /x}\n"

	tests := []struct {
		name string
		doc  string
		want []want
	}{
		{"id with a slash", ok + "id: a/b\n", []want{{config.CodeInvalidValue, "id", "an id holds only letters", ""}}},
		{"id as a number", ok + "id: 123\n", []want{{config.CodeInvalidValue, "id", "id must be a string, not a number", `write it in quotes: "123"`}}},
		{"name as a number", ok + "name: 123\n", []want{{config.CodeInvalidValue, "name", "name must be a string, not a number", `write it in quotes: "123"`}}},
		{"priority as text", ok + "priority: high\n", []want{{config.CodeInvalidValue, "priority", "priority must be an integer, not a string", ""}}},
		{"priority in quotes", ok + "priority: '5'\n", []want{{config.CodeInvalidValue, "priority", "priority must be an integer, not a string", "write it without quotes: 5"}}},
		{"enabled as text", ok + "enabled: maybe\n", []want{{config.CodeInvalidValue, "enabled", `enabled must be true or false, not "maybe"`, ""}}},
		{"enabled as a number", ok + "enabled: 2\n", []want{{config.CodeInvalidValue, "enabled", "enabled must be true or false, not 2", ""}}},
		{"negative delay", ok + "delay: -5\n", []want{{config.CodeInvalidValue, "delay", "delay must be 0 or more, not -5", ""}}},
		{"fractional delay", ok + "delay: 1.5\n", []want{{config.CodeInvalidValue, "delay", "delay must be whole milliseconds or a Go duration, not 1.5", `such as "1.5ms"`}}},
		{"delay milliseconds in quotes", ok + "delay: '1500'\n", []want{{config.CodeInvalidValue, "delay", `delay must be milliseconds or a Go duration, not "1500"`, "write 1500 without quotes"}}},
		{"signed delay", ok + "delay: +1s\n", []want{{config.CodeInvalidValue, "delay", `delay must be milliseconds or a Go duration, not "+1s"`, "such as 1500 or 1.5s"}}},
		{"repeat below one", ok + "repeat: 0\n", []want{{config.CodeInvalidValue, "repeat", "expected 1 or more", ""}}},
		{"scenario without a name", ok + "scenario: {required_state: a}\n", []want{{config.CodeMissingValue, "scenario.name", "scenario.name is not", "name the scenario"}}},
		{"scenario that isn't an object", ok + "scenario: 5\n", []want{{config.CodeInvalidValue, "scenario", "must be an object", "its keys are name, required_state, new_state"}}},
		{"on_complete as an object", ok + "on_complete: {name: x}\n", []want{{config.CodeInvalidValue, "on_complete", "must be a list, not an object", ""}}},
		{"on_complete item that isn't an object", ok + "on_complete: [{name: x}, true]\n", []want{{config.CodeInvalidValue, "on_complete[1]", "must be an object, not a bool", ""}}},
		{"on_complete item without a name", ok + "on_complete: [{parameters: {}}]\n", []want{{config.CodeMissingValue, "on_complete[0].name", "is required", "OnCompleteListener"}}},
		{"on_complete item with a typo", ok + "on_complete: [{nmae: x}]\n", []want{{config.CodeUnknownKey, "on_complete[0].nmae", "not a known setting", "did you mean on_complete[0].name?"}}},
		{"no request", "response: {status: 200}\n", []want{{config.CodeMissingValue, "request", "sets no matcher", `path: "*"`}}},
		{"empty request", "request: {}\n", []want{{config.CodeMissingValue, "request", "sets no matcher", ""}}},
		{"method as a number", "request: {method: 5}\n", []want{{config.CodeInvalidValue, "request.method", "must be a string or a matcher list", `write it in quotes: "5"`}}},
		{"empty method in a list", "request: {method: 'GET,,POST'}\n", []want{{config.CodeInvalidValue, "request.method", "holds an empty method", "separate methods with commas"}}},
		{"empty matcher list", "request: {method: []}\n", []want{{config.CodeInvalidValue, "request.method", "is an empty list", "name a matcher"}}},
		{"unknown matcher", "request: {method: [nope]}\n", []want{{config.CodeInvalidValue, "request.method", "unknown matcher", ""}}},
		{"bad url", "request: {url: 'http://a b'}\n", []want{{config.CodeInvalidValue, "request.url", "is not a URL", ""}}},
		{"url_match as a list", "request: {url_match: [equal, x]}\n", []want{{config.CodeInvalidValue, "request.url_match", "request.url_match must be a string, not a list", ""}}},
		{"url_match that doesn't compile", "request: {url_match: '('}\n", []want{{config.CodeInvalidValue, "request.url_match", `request.url_match must be a regular expression, not "("`, "error parsing regexp"}}},
		{"url matcher that doesn't build", "request: {url: [regex, '(']}\n", []want{{config.CodeInvalidValue, "request.url", "regex takes a regular expression", ""}}},
		{"url and path", "request: {url: /x, path: /y}\n", []want{{config.CodeConflict, "request.path", "request.path is set with request.url", "keep one of them"}}},
		{"path_template without its /", "request: {path_template: 'users/{id}'}\n", []want{{config.CodeInvalidValue, "request.path_template", `request.path_template="users/{id}" is not a path template: a path template starts with /`, "a parameter is a whole segment"}}},
		{"path_template with a parameter in a segment", "request: {path_template: '/users/a{id}'}\n", []want{{config.CodeInvalidValue, "request.path_template", `segment "a{id}": a parameter is a whole segment`, "such as /users/{id}"}}},
		{"path_template naming a parameter twice", "request: {path_template: '/a/{id}/b/{id}'}\n", []want{{config.CodeInvalidValue, "request.path_template", `parameter "id" is named twice`, ""}}},
		{"path_template as a list", "request: {path_template: [glob, '/users/*']}\n", []want{{config.CodeInvalidValue, "request.path_template", "request.path_template must be a string, not a list", ""}}},
		{"path_params without path_template", "request: {path: /x, path_params: {id: '7'}}\n", []want{{config.CodeMissingValue, "request.path_params", "request.path_params is set without request.path_template", "such as /users/{id}"}}},
		// The template's problem is the one: path_params can't name its parameters until it parses.
		{"path_params of a path_template that doesn't parse", "request: {path_template: 'users/{id}', path_params: {id: '7'}}\n", []want{{config.CodeInvalidValue, "request.path_template", "a path template starts with /", ""}}},
		{"path_params as a list", "request: {path: /x, path_params: [a]}\n", []want{{config.CodeInvalidValue, "request.path_params", "must be an object, not a list", ""}}},
		// As the editor schema says it.
		{"empty path_params without path_template", "request: {path: /x, path_params: {}}\n", []want{{config.CodeMissingValue, "request.path_params", "is set without request.path_template", ""}}},
		{"path_params naming no parameter", "request: {path_template: '/users/{id}', path_params: {userId: '7'}}\n", []want{{config.CodeUnknownKey, "request.path_params.userId", `request.path_params.userId names no parameter of request.path_template "/users/{id}"`, "its parameters are id"}}},
		{"path_params of a template without parameters", "request: {path_template: /users, path_params: {id: '7'}}\n", []want{{config.CodeUnknownKey, "request.path_params.id", "names no parameter", "request.path_template has no parameters"}}},
		// A relative url is exact, so a glob or a bare name in it would never match: path takes globs.
		{"relative url without its /", "request: {url: 'items?page=2'}\n", []want{{config.CodeInvalidValue, "request.url", `"items?page=2" must be an absolute URL, or start with /`, "use path instead"}}},
		// A relative url was a glob: written as one, it would match only itself.
		{"relative url with a bad escape", "request: {url: '/a%zz'}\n", []want{{config.CodeInvalidValue, "request.url", `request.url="/a%zz" is not a URL`, ""}}},
		{"relative url with a glob", "request: {url: '/users/*'}\n", []want{{config.CodeInvalidValue, "request.url", `"/users/*" has a * in its path`, "for a literal *, write %2A"}}},
		{"url, url_match and path", "request: {url: /x, url_match: x, path: /y}\n", []want{
			{config.CodeConflict, "request.url_match", "is set with request.url", ""},
			{config.CodeConflict, "request.path", "is set with request.url", ""},
		}},
		{"query value as a number", "request: {query: {page: 1}}\n", []want{{config.CodeInvalidValue, "request.query.page", "must be a string or a matcher list", `write it in quotes: "1"`}}},
		{"form value as a bool", "request: {form: {active: true}}\n", []want{{config.CodeInvalidValue, "request.form.active", "not a bool", `write it in quotes: "true"`}}},
		{"header map as a list", "request: {header: [a]}\n", []want{{config.CodeInvalidValue, "request.header", "must be an object, not a list", ""}}},
		// Found by TestPropMockFile_ServesItsModel: no request could match both, so the mock never matched.
		{"two names of one header", "request: {header: {X-Id: a, x-id: b}}\n", []want{{config.CodeConflict, "request.header.x-id", "name the same header", "header names ignore case"}}},
		{"two names of one header with many values", "request: {headers: {X-Id: [len, 1], x-id: [len, 2]}}\n", []want{{config.CodeConflict, "request.headers.x-id", "name the same header", "header names ignore case"}}},
		// Text never equals the list of a header's values: the mock would load and never match.
		{"headers value as text", "request: {headers: {Accept: text/html}}\n", []want{{config.CodeInvalidValue, "request.headers.Accept", `request.headers.Accept="text/html" is text, which never equals a list of values`, `write a matcher list, such as [contains, "text/html"]`}}},
		{"basic_auth without a password", "request: {basic_auth: {username: u}}\n", []want{{config.CodeMissingValue, "request.basic_auth.password", "request.basic_auth.password is required", ""}}},
		{"basic_auth key with a typo", "request: {basic_auth: {username: u, pasword: p}}\n", []want{
			{config.CodeUnknownKey, "request.basic_auth.pasword", "not a known setting", "did you mean request.basic_auth.password?"},
			{config.CodeMissingValue, "request.basic_auth.password", "is required", ""},
		}},
		{"basic_auth as text", "request: {basic_auth: 'u:p'}\n", []want{{config.CodeInvalidValue, "request.basic_auth", "must be an object, not a string", ""}}},
		// The host matched has no port, so text with one would never match.
		{"host with a port", "request: {host: 'api.local:8080'}\n", []want{{config.CodeInvalidValue, "request.host", `request.host="api.local:8080" has a port or brackets`, `write the address alone: "api.local"`}}},
		{"IPv6 host in brackets", "request: {host: '[::1]'}\n", []want{{config.CodeInvalidValue, "request.host", `request.host="[::1]" has a port or brackets`, `write the address alone: "::1"`}}},
		{"host as a number", "request: {host: 5}\n", []want{{config.CodeInvalidValue, "request.host", "must be a string or a matcher list", `write it in quotes: "5"`}}},
		{"client_ip matcher that doesn't build", "request: {client_ip: [nope]}\n", []want{{config.CodeInvalidValue, "request.client_ip", "unknown matcher", ""}}},
		{"client_ip with a port", "request: {client_ip: '127.0.0.1:8080'}\n", []want{{config.CodeInvalidValue, "request.client_ip", `request.client_ip="127.0.0.1:8080" has a port or brackets`, `write the address alone: "127.0.0.1"`}}},
		{"cookie value as a number", "request: {cookie: {session: 1}}\n", []want{{config.CodeInvalidValue, "request.cookie.session", "must be a string or a matcher list", `write it in quotes: "1"`}}},
		{"headers matcher that doesn't build", "request: {headers: {X-Id: [gt]}}\n", []want{{config.CodeInvalidValue, "request.headers.X-Id", "gt", ""}}},
		{"queries value as text", "request: {queries: {page: '2'}}\n", []want{{config.CodeInvalidValue, "request.queries.page", `request.queries.page="2" is text, which never equals a list of values`, `[contains, "2"]`}}},
		{"forms value as text", "request: {forms: {tag: a}}\n", []want{{config.CodeInvalidValue, "request.forms.tag", "is text, which never equals a list of values", `[contains, "a"]`}}},
		{"form_file as a list", "request: {form_file: [a]}\n", []want{{config.CodeInvalidValue, "request.form_file", "must be an object, not a list", ""}}},
		{"file pattern as text", "request: {form_file: {avatar: a.png}}\n", []want{{config.CodeInvalidValue, "request.form_file.avatar", "must be an object, not a string", ""}}},
		{"file pattern key with a typo", "request: {form_file: {avatar: {filname: a.png}}}\n", []want{{config.CodeUnknownKey, "request.form_file.avatar.filname", "not a known setting", "did you mean request.form_file.avatar.filename?"}}},
		{"filename as a number", "request: {form_file: {avatar: {filename: 1}}}\n", []want{{config.CodeInvalidValue, "request.form_file.avatar.filename", "must be a string or a matcher list", `write it in quotes: "1"`}}},
		{"file body and body_file", "request: {form_file: {avatar: {body: x, body_file: a.png}}}\n", []want{{config.CodeConflict, "request.form_file.avatar.body_file", "request.form_file.avatar.body_file is set with request.form_file.avatar.body", "keep one of them"}}},
		{"file body_file that doesn't exist", "request: {form_file: {avatar: {body_file: no-such-file.png}}}\n", []want{{config.CodeFileNotFound, "request.form_file.avatar.body_file", `"no-such-file.png" does not exist`, "relative paths start at the mock file's directory"}}},
		{"empty file body_file", "request: {form_file: {avatar: {body_file: ''}}}\n", []want{{config.CodeInvalidValue, "request.form_file.avatar.body_file", "expected a file name", ""}}},
		{"file body_file that is a directory", "request: {form_file: {avatar: {body_file: .}}}\n", []want{{config.CodeReadError, "request.form_file.avatar.body_file", "is a directory", ""}}},
		{"file body and body_file, and a typo", "request: {form_file: {avatar: {body: x, body_file: a.png, filname: b}}}\n", []want{
			{config.CodeConflict, "request.form_file.avatar.body_file", "is set with request.form_file.avatar.body", "keep one of them"},
			{config.CodeUnknownKey, "request.form_file.avatar.filname", "not a known setting", "did you mean request.form_file.avatar.filename?"},
		}},
		{"file every as text", "request: {form_file: {avatar: {every: maybe}}}\n", []want{{config.CodeInvalidValue, "request.form_file.avatar.every", `must be true or false, not "maybe"`, ""}}},
		{"two names of one file header", "request: {form_file: {avatar: {header: {X-Id: a, x-id: b}}}}\n", []want{{config.CodeConflict, "request.form_file.avatar.header.x-id", "name the same header", "header names ignore case"}}},
		{"bad pattern in a list", "request: {form_file: {avatar: [{}, {every: x}]}}\n", []want{{config.CodeInvalidValue, "request.form_file.avatar[1].every", "must be true or false", ""}}},
		{"body as an object", "request: {body: {a: 1}}\n", []want{{config.CodeInvalidValue, "request.body", "must be a string or a matcher list", ""}}},
		{"matcher without its argument", "request: {header: {X-Id: [gt]}}\n", []want{{config.CodeInvalidValue, "request.header.X-Id", "gt", ""}}},
		{"multi-matcher entry that isn't a matcher", "request: {body: [all, [true]]}\n", []want{{config.CodeInvalidValue, "request.body", "a matcher is a name or a definition", ""}}},
		{"unknown top-level key", ok + "requset: {}\n", []want{{config.CodeUnknownKey, "requset", "not a known setting", "did you mean request?"}}},
		{"dotted key", "request.path: /x\nrequest: {method: GET}\n", []want{{config.CodeUnknownKey, "request.path", "written as a dotted key", "nest it"}}},
		{"response and proxy", ok + "response: {status: 200}\nproxy: {target: 'http://a'}\n", []want{{config.CodeConflict, "proxy", "proxy is set with response", "keep one of them"}}},
		// A proxy never follows redirects, so the setting that claimed to turn that off is gone.
		{"proxy no_follow", ok + "proxy: {target: 'http://a', no_follow: true}\n", []want{{config.CodeUnknownKey, "proxy.no_follow", "not a known setting", ""}}},
		{"response that isn't an object", ok + "response: 5\n", []want{{config.CodeInvalidValue, "response", "must be an object, not a number", ""}}},
		{"response key with a typo", ok + "response: {stauts: 201}\n", []want{{config.CodeUnknownKey, "response.stauts", "not a known setting", "did you mean response.status?"}}},
		{"response key with no near match", ok + "response: {colour: red}\n", []want{{config.CodeUnknownKey, "response.colour", "", "the keys under response are status, header"}}},
		{"status out of range", ok + "response: {status: 42}\n", []want{{config.CodeInvalidValue, "response.status", "from 100 to 599", ""}}},
		{"header value as a number", ok + "response: {header: {X-Count: 10}}\n", []want{{config.CodeInvalidValue, "response.header.X-Count", "must be a string or a list of strings", `write it in quotes: "10"`}}},
		{"header list holding a number", ok + "response: {header: {X: [a, 1]}}\n", []want{{config.CodeInvalidValue, "response.header.X[1]", "response.header.X[1] must be a string, not a number", `write it in quotes: "1"`}}},
		{"trailer value as a list of numbers", ok + "response: {trailer: {x: [1]}}\n", []want{{config.CodeInvalidValue, "response.trailer.x[0]", "must be a string, not a number", ""}}},
		{"null body", ok + "response: {body: null}\n", []want{{config.CodeInvalidValue, "response.body", "is null", `write "" for an empty body`}}},
		{"body and body_file", ok + "response: {body: x, body_file: y.txt}\n", []want{{config.CodeConflict, "response.body_file", "response.body_file is set with response.body", ""}}},
		{"body_base64 that isn't base64", ok + "response: {body_base64: '//4'}\n", []want{{config.CodeInvalidValue, "response.body_base64", "response.body_base64 isn't standard base64", "padding included"}}},
		{"body_base64 as a number", ok + "response: {body_base64: 5}\n", []want{{config.CodeInvalidValue, "response.body_base64", "response.body_base64 must be a string, not a number", ""}}},
		{"body and body_base64", ok + "response: {body: x, body_base64: eA==}\n", []want{{config.CodeConflict, "response.body_base64", "response.body_base64 is set with response.body", "keep one of them"}}},
		{"body_base64 and body_file", ok + "response: {body_base64: eA==, body_file: y.txt}\n", []want{{config.CodeConflict, "response.body_file", "response.body_file is set with response.body_base64", ""}}},
		{"templated base64 body", ok + "response: {body_base64: eA==, template: {enabled: true}}\n", []want{{config.CodeConflict, "response.template.enabled", "response.template.enabled is set with response.body_base64, which isn't text", "write the body as a string"}}},
		{"empty body_file", ok + "response: {body_file: ''}\n", []want{{config.CodeInvalidValue, "response.body_file", "expected a file name", ""}}},
		{"template data as a number", ok + "response: {template: {data: 5}}\n", []want{{config.CodeInvalidValue, "response.template.data", "must be an object or a list", ""}}},
		{"templated JSON body", ok + "response: {body: {a: 1}, template: {enabled: true}}\n", []want{{config.CodeConflict, "response.template.enabled", "response.template.enabled is set with response.body, which isn't text", "write the body as a string"}}},
		{"multipart as text", ok + "response: {multipart: x}\n", []want{{config.CodeInvalidValue, "response.multipart", "must be an object, not a string", ""}}},
		{"multipart and body", ok + "response: {body: x, multipart: {type: mixed, parts: [{}]}}\n", []want{{config.CodeConflict, "response.multipart", "response.multipart is set with response.body", "keep one of them"}}},
		{"multipart and body_file", ok + "response: {body_file: a.txt, multipart: {type: mixed, parts: [{}]}}\n", []want{{config.CodeConflict, "response.multipart", "response.multipart is set with response.body_file", ""}}},
		{"multipart and a Content-Type header", ok + "response: {header: {content-type: text/plain}, multipart: {type: mixed, parts: [{}]}}\n", []want{{config.CodeConflict, "response.header.content-type", "response.header.content-type is set with response.multipart", "a multipart body sets the Content-Type: remove the header"}}},
		{"multipart and a Content-Type header template", ok + "response: {header_template: {Content-Type: x}, multipart: {type: mixed, parts: [{}]}}\n", []want{{config.CodeConflict, "response.header_template.Content-Type", "is set with response.multipart", ""}}},
		{"multipart without a type", ok + "response: {multipart: {parts: [{}]}}\n", []want{{config.CodeMissingValue, "response.multipart.type", "response.multipart.type is required", "name the subtype"}}},
		{"multipart without parts", ok + "response: {multipart: {type: mixed}}\n", []want{{config.CodeMissingValue, "response.multipart.parts", "response.multipart.parts is required", "add at least one part"}}},
		// json/v2 words the rest of the message at random: "cannot" or "unable to".
		{"part body JSON can't write", ok + "response: {multipart: {type: mixed, parts: [{body: .nan}]}}\n", []want{{config.CodeInvalidValue, "response.multipart.parts[0].body", `response.multipart.parts[0].body="NaN": json: `, ""}}},
		{"form-data part without a name", ok + "response: {multipart: {type: form-data, parts: [{body: x}]}}\n", []want{{config.CodeMissingValue, "response.multipart.parts[0].name", "is required in a form-data part", ""}}},
		{"multipart in a sequence", ok + "response_sequence: {responses: [{multipart: {type: 'multipart/mixed', parts: [{}]}}]}\n", []want{{config.CodeInvalidValue, "response_sequence.responses[0].multipart.type", `must be a subtype, not "multipart/mixed"`, "write the subtype only"}}},
		{"template that isn't an object", ok + "response: {template: 5}\n", []want{{config.CodeInvalidValue, "response.template", "must be an object", "its keys are enabled, data"}}},
		{"unsupported encoding", ok + "response: {encoding: br}\n", []want{{config.CodeInvalidValue, "response.encoding", `response.encoding must be gzip, not "br"`, ""}}},
		{"encoding in capitals", ok + "response: {encoding: GZIP}\n", []want{{config.CodeInvalidValue, "response.encoding", `response.encoding must be gzip, not "GZIP"`, ""}}},
		{"sequence without responses", ok + "response_sequence: {}\n", []want{{config.CodeMissingValue, "response_sequence.responses", "is required", ""}}},
		{"sequence with no responses", ok + "response_sequence: {responses: []}\n", []want{{config.CodeInvalidValue, "response_sequence.responses", "is empty", "add at least one reply"}}},
		{"sequence reply with a bad status", ok + "response_sequence: {responses: [{status: 200}, {status: 1}]}\n", []want{{config.CodeInvalidValue, "response_sequence.responses[1].status", "from 100 to 599", ""}}},
		{"after_ended with a typo", ok + "response_sequence: {responses: [{}], after_ended: {bdy: x}}\n", []want{{config.CodeUnknownKey, "response_sequence.after_ended.bdy", "", "did you mean response_sequence.after_ended.body?"}}},
		{"sequence that isn't an object", ok + "response_sequence: 5\n", []want{{config.CodeInvalidValue, "response_sequence", "must be an object", ""}}},
		{"random seed as text", ok + "response_random: {responses: [{}], seed: x}\n", []want{{config.CodeInvalidValue, "response_random.seed", "response_random.seed must be an integer, not a string", ""}}},
		{"random without responses", ok + "response_random: {seed: 1}\n", []want{{config.CodeMissingValue, "response_random.responses", "is required", ""}}},
		{"proxy without a target", ok + "proxy: {timeout: 10}\n", []want{{config.CodeMissingValue, "proxy.target", "is required", ""}}},
		{"proxy target that isn't absolute", ok + "proxy: {target: /x}\n", []want{{config.CodeInvalidValue, "proxy.target", "expected an absolute http or https URL", ""}}},
		{"proxy target that isn't http", ok + "proxy: {target: 'ftp://files.local'}\n", []want{{config.CodeInvalidValue, "proxy.target", "expected an absolute http or https URL", ""}}},
		{"negative proxy timeout", ok + "proxy: {target: 'http://a', timeout: -1}\n", []want{{config.CodeInvalidValue, "proxy.timeout", "proxy.timeout must be 0 or more, not -1", ""}}},
		{"remove_request_headers as an object", ok + "proxy: {target: 'http://a', remove_request_headers: {a: 1}}\n", []want{{config.CodeInvalidValue, "proxy.remove_request_headers", "proxy.remove_request_headers must be a list of strings, not an object", ""}}},
		{"remove_request_headers as text", ok + "proxy: {target: 'http://a', remove_request_headers: 'a, b'}\n", []want{{config.CodeInvalidValue, "proxy.remove_request_headers", "must be a list of strings, not a string", `write a list: ["a", "b"]`}}},
		{"target as a number", ok + "proxy: {target: 5}\n", []want{{config.CodeInvalidValue, "proxy.target", "proxy.target must be a string, not a number", `write it in quotes: "5"`}}},
		{"ext as a list", ok + "ext: [1]\n", []want{{config.CodeInvalidValue, "ext", "must be an object, not a list", ""}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := decodeDoc(m, tc.doc)
			require.Nil(t, mock)

			problems := mockProblems(t, err)
			require.Len(t, problems, len(tc.want), "%v", problems)

			for _, w := range tc.want {
				requireProblem(t, problems, w)
			}
		})
	}
}

func TestMockSchema_EveryProblemAtOnce(t *testing.T) {
	// A file is fixed in one pass: every problem is listed, not only the first.
	_, err := decodeDoc(MustNew(Setup().Silent()), `
requset: {}
request: {method: 5, path: /x, url: /y}
response: {status: 42, stauts: 1}
proxy: {target: 'http://a'}
`)

	got := make([]string, 0, 6)
	for _, p := range mockProblems(t, err) {
		got = append(got, p.Code+" "+p.Key)
	}

	require.ElementsMatch(t, []string{
		"unknown_key requset",
		"invalid_value request.method",
		"conflict request.path",
		"invalid_value response.status",
		"unknown_key response.stauts",
		"conflict proxy",
	}, got)
}

func TestMockSchema_ErrorText(t *testing.T) {
	// The whole error, as users read it: one line per problem, sorted, with the origin and the fix.
	_, err := FromBytes([]byte("request: {query: {page: 1}}\nresponse: {stauts: 201, body: x, body_file: y}\nrepeat: 0\n"), "yaml").
		Build(MustNew(Setup().Silent()))

	require.EqualError(t, err, `mock: 4 problems
  bytes: invalid_value: repeat="0": expected 1 or more
  bytes: invalid_value: request.query.page must be a string or a matcher list, such as [equal, x], not a number; write it in quotes: "1"
  bytes: conflict: response.body_file is set with response.body; keep one of them
  bytes: unknown_key: response.stauts is not a known setting; did you mean response.status?`)
}

func TestMockSchema_FormsAndMultipartErrorText(t *testing.T) {
	// Golden: the words of the form_file and multipart problems, as a user reads them, sorted by key.
	_, err := decodeDoc(MustNew(Setup().Silent()), `
request:
  path: /upload
  form_file:
    avatar: {filname: a.png, every: maybe}
response:
  body: x
  header: {Content-Type: text/plain}
  multipart:
    type: multipart/mixed
    parts:
      - header: {X-N: "a\nb"}
        delay: -5
`)
	require.EqualError(t, err, `mock: 7 problems
  doc: invalid_value: request.form_file.avatar.every must be true or false, not "maybe"
  doc: unknown_key: request.form_file.avatar.filname is not a known setting; did you mean request.form_file.avatar.filename?
  doc: conflict: response.header.Content-Type is set with response.multipart; a multipart body sets the Content-Type: remove the header
  doc: conflict: response.multipart is set with response.body; keep one of them
  doc: invalid_value: response.multipart.parts[0].delay must be 0 or more, not -5
  doc: invalid_value: response.multipart.parts[0].header.X-N holds a line break
  doc: invalid_value: response.multipart.type must be a subtype, not "multipart/mixed"; write the subtype only, such as mixed`)
}

func TestMockSchema_ReadProblems(t *testing.T) {
	m := MustNew(Setup().Silent())

	t.Run("syntax error points to the line, without noise from checks", func(t *testing.T) {
		_, err := mockDoc{origin: "doc", format: "json", data: []byte("{\n  \"request\": \n}")}.build(m)

		problems := mockProblems(t, err)
		require.Len(t, problems, 1)
		require.Equal(t, config.CodeParseError, problems[0].Code)
		require.Equal(t, "doc:3:1", problems[0].Origin)
	})

	t.Run("unsupported format", func(t *testing.T) {
		_, err := FromBytes([]byte("a = 1"), "toml").Build(m)
		require.Equal(t, config.CodeUnsupportedFormat, mockProblems(t, err)[0].Code)
	})

	t.Run("a file with another extension", func(t *testing.T) {
		_, err := FromFile("testdata/loader/02.txt").Build(m)
		problems := mockProblems(t, err)

		require.Equal(t, config.CodeUnsupportedFormat, problems[0].Code)
		require.Equal(t, "file testdata/loader/02.txt", problems[0].Origin)
	})

	t.Run("a missing file", func(t *testing.T) {
		_, err := FromFile("testdata/missing.mock.yaml").Build(m)
		problems := mockProblems(t, err)

		require.Equal(t, config.CodeFileNotFound, problems[0].Code)
		require.Contains(t, problems[0].Hint, "relative paths start at")
	})

	t.Run("a reply template that doesn't compile fails the mock", func(t *testing.T) {
		// Templates compile when the mock is built, so a broken one fails the file, not each request.
		doc := "request: {path: /x}\nresponse: {body: '{{ .Request.Method', template: {enabled: true}}\n"
		_, err := FromBytes([]byte(doc), "yaml").Build(m)

		problems := mockProblems(t, err)
		require.Len(t, problems, 1)
		require.Equal(t, config.CodeInvalidValue, problems[0].Code)
		require.Equal(t, "reply: template: body:1: unclosed action", problems[0].Message)
	})

	t.Run("an unregistered listener", func(t *testing.T) {
		_, err := decodeDoc(m, "request: {path: /x}\non_complete: [{name: nobody}]\n")
		require.Contains(t, mockProblems(t, err)[0].Message, "on_complete listener nobody at index 0 is not registered")
	})
}

type capturingHandler struct {
	got map[string]any
	err error
}

func (h *capturingHandler) Handle(fields map[string]any, _ *Builder) error {
	h.got = fields
	return h.err
}

func TestMockFile_ExtKeepsKeysAndNumbers(t *testing.T) {
	// B6: ext reaches handlers with its keys as written, and numbers in one form for JSON and YAML.
	for _, format := range []string{"json", "yaml"} {
		h := &capturingHandler{}
		m := MustNew(Setup().Silent().MockFileHandlers(h))

		doc := map[string]string{
			"json": `{"request": {"path": "/x"}, "ext": {"userId": 7, "Nested": {"camelCase": [1.5, "2024-01-01"]}}}`,
			"yaml": "request: {path: /x}\next: {userId: 7, Nested: {camelCase: [1.5, 2024-01-01]}}\n",
		}[format]

		_, err := FromBytes([]byte(doc), format).Build(m)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"userId": int64(7), "Nested": map[string]any{"camelCase": []any{1.5, "2024-01-01"}}}, h.got, format)
	}
}

func TestMockFile_ExtHandlerErrors(t *testing.T) {
	h := &capturingHandler{err: errors.New("unknown flag")}
	m := MustNew(Setup().Silent().MockFileHandlers(h))

	_, err := FromBytes([]byte("request: {path: /x}\next: {a: 1}\n"), "yaml").Build(m)

	problems := mockProblems(t, err)
	require.Equal(t, "ext", problems[0].Key)
	require.Equal(t, "mock file handler 0: unknown flag", problems[0].Message)
}

func TestMockFile_HonorsID(t *testing.T) {
	// F15: a file names its mock, so it can be found, replaced or deleted by that name.
	m := MustNew(Setup().Silent())

	mock, err := decodeDoc(m, "id: users-get\nrequest: {path: /users}\n")
	require.NoError(t, err)
	require.Equal(t, "users-get", mock.ID())

	_, err = m.Mock(FromBytes([]byte("id: same\nrequest: {path: /a}\n"), "yaml"))
	require.NoError(t, err)

	_, err = m.Mock(FromBytes([]byte("id: same\nrequest: {path: /b}\n"), "yaml"))
	require.ErrorIs(t, err, vlatte.ErrDuplicateID)
}

func TestMockFile_BuildIsRepeatable(t *testing.T) {
	// F2: building twice gives two mocks, not one mock with doubled matchers.
	m := MustNew(Setup().Silent())
	b := FromFile("testdata/loader/01.mock.json")

	first, err := b.Build(m)
	require.NoError(t, err)
	second, err := b.Build(m)
	require.NoError(t, err)

	require.NotSame(t, first, second)
	require.NotEqual(t, first.ID(), second.ID())
	require.Len(t, second.spec.expectations, len(first.spec.expectations))
	require.Equal(t, strings.Count(describe(t, first), "\""), strings.Count(describe(t, second), "\""))
}

func describe(t require.TestingT, m *Mock) string {
	b, err := m.MarshalJSON()
	require.NoError(t, err)

	return string(b)
}

func TestMockFile_Regressions(t *testing.T) {
	// Bugs of the viper decoder, by the IDs in docs/.ai/followups-plan.md.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	get := func(t *testing.T, doc, path string) (*http.Response, string) {
		t.Helper()

		scope, err := m.Mock(FromBytes([]byte(doc), "yaml"))
		require.NoError(t, err)
		t.Cleanup(func() { scope.Clear(context.Background()) })

		// The path may hold a query, which URL would escape.
		res, err := http.Get(m.URL() + path)
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res, string(b)
	}

	t.Run("B5: int, list and object bodies are served as JSON", func(t *testing.T) {
		for path, body := range map[string]string{"/int": "42", "/list": "[1,2]", "/object": `{"a":[true,1.5]}`} {
			_, got := get(t, "request: {path: "+path+"}\nresponse: {body: "+body+"}\n", path)
			require.JSONEq(t, body, got, path)
		}
	})

	t.Run("B6: query names keep their case", func(t *testing.T) {
		res, _ := get(t, "request: {path: /case, query: {userId: '7'}}\n", "/case?userId=7")
		require.Equal(t, http.StatusOK, res.StatusCode)

		res, err := http.Get(m.URL() + "/case?userid=7")
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, StatusNoMatch, res.StatusCode)
	})

	t.Run("C2: proxy.timeout numbers are milliseconds", func(t *testing.T) {
		mock, err := decodeDoc(m, "request: {path: /x}\nproxy: {target: 'http://localhost', timeout: 1500}\n")
		require.NoError(t, err)

		proxy, ok := mock.reply.(*proxyReplier)
		require.True(t, ok)
		require.Equal(t, 1500*time.Millisecond, proxy.timeout)
	})

	t.Run("F9: ssl_verify true verifies the target", func(t *testing.T) {
		target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("target")) }))
		t.Cleanup(target.Close)

		// A reply that fails answers with the no-match status and the error.
		res, body := get(t, "request: {path: /verify}\nproxy: {target: '"+target.URL+"', ssl_verify: true}\n", "/verify")
		require.Equal(t, StatusNoMatch, res.StatusCode)
		require.Contains(t, body, "failed to verify certificate")

		res, body = get(t, "request: {path: /skip}\nproxy: {target: '"+target.URL+"', ssl_verify: false}\n", "/skip")
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "target", body)
	})
}

func TestMockFile_FormFieldsComeFromTheBody(t *testing.T) {
	// Found by TestPropMockFile_ServesItsModel at 10000 checks: form fields were read from http.Request.Form, which
	// also holds the query, so a query parameter could stand in for a form field that the body didn't have.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request:\n  method: POST\n  path: /form\n  form: {H: [present], A: '-'}\n"), "yaml"))

	post := func(query, form string) int {
		res, err := http.Post(m.URL()+"/form?"+query, MIMEFormURLEncoded, strings.NewReader(form))
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	require.Equal(t, http.StatusOK, post("", "H=x&A=-"))
	require.Equal(t, StatusNoMatch, post("H=x", "A=-"), "H is in the query, not in the form")
	require.Equal(t, StatusNoMatch, post("A=-", "H=x"), "A is in the query, not in the form")
}

func TestMockFile_ServesTrailers(t *testing.T) {
	// A trailer of a mock file reaches the client after the body.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request: {path: /t}\nresponse:\n  body: ok\n  trailer: {X-End: done}\n"), "yaml"))

	res, err := http.Get(m.URL() + "/t")
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, "ok", string(body))
	require.Equal(t, "done", res.Trailer.Get("X-End"))
}

func TestMockFile_ServesABase64BodyAsItsBytes(t *testing.T) {
	// #42: a body that isn't UTF-8, such as an image or text in another encoding, has no place in body, whose text
	// JSON and YAML hold as UTF-8. body_base64 holds any bytes, as WireMock's base64Body does.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request: {path: /b}\nresponse:\n  body_base64: //4A\n"), "yaml"))
	m.MustMock(FromBytes([]byte("request: {path: /empty}\nresponse:\n  body_base64: ''\n"), "yaml"))

	res, body := fetch(t, m.URL("/b"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []byte{0xff, 0xfe, 0x00}, body)

	res, body = fetch(t, m.URL("/empty"))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, body)
}

func TestMockFile_ServesAnyJSONValueAsItsJSON(t *testing.T) {
	// A body that isn't text is sent as JSON, whatever kind of value it is and whichever format the file is in:
	// a top-level list or an integer was once dropped without a word (B5). Text is sent as is, so a JSON string
	// or null is written as text.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	tests := []struct {
		name   string
		format string
		body   string
		want   string
	}{
		{"JSON object", "json", `{"b": 1, "a": "x"}`, `{"a":"x","b":1}`},
		{"JSON empty object", "json", `{}`, `{}`},
		{"JSON list", "json", `[1, 2, 3]`, `[1,2,3]`},
		{"JSON empty list", "json", `[]`, `[]`},
		{"JSON list of objects", "json", `[{"a": 1}, {"b": [true, null]}]`, `[{"a":1},{"b":[true,null]}]`},
		{"JSON integer", "json", `42`, `42`},
		{"JSON integer past float64 precision", "json", `9007199254740993`, `9007199254740993`},
		{"JSON negative integer", "json", `-7`, `-7`},
		{"JSON float", "json", `1.5`, `1.5`},
		{"JSON true", "json", `true`, `true`},
		{"JSON false", "json", `false`, `false`},
		{"JSON nested values", "json", `{"a": {"b": [1, 2.5, "x", false, null, {"c": []}]}}`, `{"a":{"b":[1,2.5,"x",false,null,{"c":[]}]}}`},
		{"JSON string as text", "json", `"hi"`, `hi`},
		{"JSON string literal", "json", `"\"hi\""`, `"hi"`},
		{"JSON null literal", "json", `"null"`, `null`},
		{"YAML object", "yaml", `{b: 1, a: x}`, `{"a":"x","b":1}`},
		{"YAML block object", "yaml", "\n    b: 1\n    a: x", `{"a":"x","b":1}`},
		{"YAML empty object", "yaml", `{}`, `{}`},
		{"YAML list", "yaml", `[1, 2, 3]`, `[1,2,3]`},
		{"YAML empty list", "yaml", `[]`, `[]`},
		{"YAML block list of objects", "yaml", "\n    - a: 1\n    - b: [true, null]", `[{"a":1},{"b":[true,null]}]`},
		{"YAML integer", "yaml", `42`, `42`},
		{"YAML integer past float64 precision", "yaml", `9007199254740993`, `9007199254740993`},
		{"YAML negative integer", "yaml", `-7`, `-7`},
		{"YAML float", "yaml", `1.5`, `1.5`},
		{"YAML true", "yaml", `true`, `true`},
		{"YAML false", "yaml", `false`, `false`},
		{"YAML nested values", "yaml", `{a: {b: [1, 2.5, x, false, null, {c: []}]}}`, `{"a":{"b":[1,2.5,"x",false,null,{"c":[]}]}}`},
		{"YAML string as text", "yaml", `hi`, `hi`},
		{"YAML string literal", "yaml", `'"hi"'`, `"hi"`},
		{"YAML null literal", "yaml", `'null'`, `null`},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := fmt.Sprintf("/%d", i)
			doc := "request: {path: " + path + "}\nresponse:\n  body: " + tc.body + "\n"
			if tc.format == "json" {
				doc = `{"request": {"path": "` + path + `"}, "response": {"body": ` + tc.body + `}}`
			}

			scope, err := m.Mock(FromBytes([]byte(doc), tc.format))
			require.NoError(t, err)
			t.Cleanup(func() { scope.Clear(context.Background()) })

			res, err := http.Get(m.URL(path))
			require.NoError(t, err)

			got, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			require.Equal(t, http.StatusOK, res.StatusCode)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestMockFile_RefusesANullBodyInJSON(t *testing.T) {
	// A null body is refused, as in YAML, rather than served empty: an empty body is written "", and JSON null "null".
	m := MustNew(Setup().Silent())

	mock, err := mockDoc{origin: "doc", format: "json", data: []byte(`{"request": {"path": "/x"}, "response": {"body": null}}`)}.build(m)
	require.Nil(t, mock)

	problems := mockProblems(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, config.CodeInvalidValue, problems[0].Code)
	require.Equal(t, "response.body", problems[0].Key)
	require.Equal(t, "response.body is null", problems[0].Message)
}

func TestMockFile_ProxyKeys(t *testing.T) {
	// Every proxy key reaches the forwarded request or the response.
	var forwarded *http.Request

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r
		w.Write([]byte("target"))
	}))
	t.Cleanup(target.Close)

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	doc := `
request: {path: /api/users.json}
proxy:
  target: ` + target.URL + `
  request_header: {X-Fwd: a}
  header: {X-Res: [b, c]}
  remove_request_headers: [X-Drop]
  trim_prefix: /api
  trim_suffix: .json
  timeout: 1500
  ssl_verify: false
`
	scope, err := m.Mock(FromBytes([]byte(doc), "yaml"))
	require.NoError(t, err)

	proxy, ok := scope.Mocks()[0].reply.(*proxyReplier)
	require.True(t, ok)
	require.Equal(t, 1500*time.Millisecond, proxy.timeout)

	req, err := http.NewRequest(http.MethodGet, m.URL("/api/users.json"), nil)
	require.NoError(t, err)
	req.Header.Set("X-Drop", "secret")

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, []string{"b", "c"}, res.Header.Values("X-Res"))
	require.Equal(t, "/users", forwarded.URL.Path)
	require.Equal(t, "a", forwarded.Header.Get("X-Fwd"))
	require.Empty(t, forwarded.Header.Get("X-Drop"))
}

func TestMockSchema_MoreReadProblems(t *testing.T) {
	m := MustNew(Setup().Silent())

	t.Run("a directory named like a mock file", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "x.mock.yaml")
		require.NoError(t, os.Mkdir(dir, 0o700))

		_, err := FromFile(dir).Build(m)
		require.Equal(t, config.CodeReadError, mockProblems(t, err)[0].Code)
	})

	t.Run("{{ is text outside templates", func(t *testing.T) {
		// A mock file is data: only template settings compile, so a body without template.enabled keeps its {{.
		srv := MustNew(Setup().Silent())
		srv.MustStart()
		t.Cleanup(srv.Close)

		srv.MustMock(FromBytes([]byte("request: {path: /x}\nresponse: {body: '{{ .Request.Method }}'}\n"), "yaml"))

		status, body := get(t, srv, "/x")
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, "{{ .Request.Method }}", body)
	})

	t.Run("a missing absolute path needs no hint", func(t *testing.T) {
		_, err := FromFile(filepath.Join(t.TempDir(), "missing.mock.yaml")).Build(m)

		problems := mockProblems(t, err)
		require.Equal(t, config.CodeFileNotFound, problems[0].Code)
		require.Empty(t, problems[0].Hint)
	})
}

func TestMockFile_RelativeURLIsExact(t *testing.T) {
	// A relative url was a glob of the path, and its query was ignored: /items answered /items?page=2, and a mock
	// file for one page answered them all. It names the exact path and query now.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(
		FromBytes([]byte("request: {url: '/items?page=2&sort=asc'}\nresponse: {status: 200, body: page 2}\n"), "yaml"),
		FromBytes([]byte("request: {url: /items}\nresponse: {status: 200, body: all}\n"), "yaml"),
		FromBytes([]byte("request: {url: '/files/%2A?q=*'}\nresponse: {status: 200, body: star}\n"), "yaml"),
	)

	tests := []struct {
		target string
		status int
		body   string
	}{
		{target: "/items?page=2&sort=asc", status: http.StatusOK, body: "page 2"},
		{target: "/items?sort=asc&page=2", status: http.StatusOK, body: "page 2"},
		{target: "/items", status: http.StatusOK, body: "all"},
		{target: "/items?page=3&sort=asc", status: StatusNoMatch},
		{target: "/items?page=2", status: StatusNoMatch},
		{target: "/items?page=2&sort=asc&x=1", status: StatusNoMatch},
		{target: "/items/", status: StatusNoMatch},
		{target: "/files/*?q=*", status: http.StatusOK, body: "star"},
		{target: "/files/%2A?q=*", status: http.StatusOK, body: "star"},
		{target: "/files/a?q=*", status: StatusNoMatch},
	}

	for _, tc := range tests {
		status, body := get(t, m, tc.target)
		require.Equal(t, tc.status, status, tc.target)

		if tc.status == http.StatusOK {
			require.Equal(t, tc.body, body, tc.target)
		}
	}
}

// hasHeader builds the custom request matcher [has-header, name]: a request that has the header name.
func hasHeader(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
	if len(args) != 1 {
		return nil, errors.New("takes a header name")
	}

	name, ok := args[0].(string)
	if !ok {
		return nil, errors.New("takes a header name")
	}

	return match.Function(func(v any) (bool, error) {
		r, isRequest := v.(*http.Request)
		return isRequest && r.Header.Get(name) != "", nil
	}), nil
}

func TestMockFile_CustomMatchesTheWholeRequest(t *testing.T) {
	// A custom matcher: a matcher of the server's registry, by name with its arguments, gets the request.
	m := MustNew(Setup().Silent().Matchers(matchbuild.MustNew(matchbuild.WithMatcher("has-header", hasHeader))))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request: {path: /a, custom: [[has-header, X-Admin], [not, [has-header, X-Banned]]]}\nresponse: {status: 200}\n"), "yaml"))

	tests := []struct {
		header http.Header
		status int
	}{
		{http.Header{"X-Admin": {"1"}}, http.StatusOK},
		{http.Header{}, StatusNoMatch},
		{http.Header{"X-Admin": {"1"}, "X-Banned": {"1"}}, StatusNoMatch},
	}

	for _, tc := range tests {
		res, _ := do(t, http.MethodGet, m.URL("/a"), tc.header, nil)
		require.Equal(t, tc.status, res.StatusCode, "%v", tc.header)
	}
}

func TestMockFile_CustomProblems(t *testing.T) {
	app := MustNew(Setup().Silent().Matchers(matchbuild.MustNew(matchbuild.WithMatcher("has-header", hasHeader))))
	t.Cleanup(app.Close)

	tests := []struct {
		doc  string
		want []string
	}{
		// custom is a list of specs: one spec alone is a list of a name and a header, read as two specs.
		{"request: {custom: [has-header, X-Admin]}\nresponse: {status: 200}", []string{
			`request.custom[0]="has-header": has-header: takes a header name`, `request.custom[1]="X-Admin": unknown matcher "X-Admin"`,
		}},
		{"request: {custom: [[nope]]}\nresponse: {status: 200}", []string{`unknown matcher "nope"`}},
		{"request: {custom: [1]}\nresponse: {status: 200}", []string{"a matcher list, such as [is-admin], or a matcher's name"}},
		{"request: {custom: has-header}\nresponse: {status: 200}", []string{"a list"}},
	}

	for _, tc := range tests {
		_, err := decodeDoc(app, tc.doc)
		problems := mockProblems(t, err)
		require.Len(t, problems, len(tc.want), tc.doc)

		for i, want := range tc.want {
			require.Contains(t, problems[i].Message, want, tc.doc)
		}
	}
}

func TestMockFile_StepsProblems(t *testing.T) {
	// A fault spec that can't be built fails the file, at its key, with what is wrong.
	app := MustNew(Setup().Silent())
	t.Cleanup(app.Close)

	tests := []struct {
		doc  string
		key  string
		want string
	}{
		{
			"steps: [{type: slow_close, delay: 1s}, {type: bandwidth, rate: 0}]", "steps[1]",
			"steps[1]: bandwidth needs a byte per second at least, not 0",
		},
		{
			"wire_steps: [{type: nope}]", "wire_steps[0].type",
			"wire_steps[0].type must be one of empty_response, malformed_response_chunk, random_data_then_close, " +
				"connection_reset_by_peer, chunked_dribble_delay, latency, bandwidth, slicer, slow_close, timeout, " +
				`limit_data or connection_reset_by_peer_after, not "nope"`,
		},
		{"upstream_steps: [{type: latency}]", "upstream_steps[0].latency", "upstream_steps[0].latency is required by a latency fault"},
		{"steps: [{type: slow_close, dely: 1s, delay: 1s}]", "steps[0].dely", "steps[0].dely is not a known setting"},
		{"steps: [[slow_close, 1s]]", "steps[0]", "steps[0] must be an object, such as {type: latency, latency: 300ms}, not a list"},
		{"steps: empty_response", "steps", "steps must be a list, not a string"},
	}

	for _, tc := range tests {
		_, err := decodeDoc(app, "request: {path: /f}\nresponse: {status: 200}\n"+tc.doc)
		problems := mockProblems(t, err)
		require.Len(t, problems, 1, tc.doc)
		require.Equal(t, tc.key, problems[0].Key, tc.doc)
		require.Equal(t, tc.want, problems[0].Message, tc.doc)
	}
}

// http.schema.json is for editors: the decoder doesn't read it. These tests keep the two in step.

const _schemaURL = "mem://vlatte/mock.schema.json"

func loadJSONSchema(t require.TestingT) (*jsonschema.Schema, map[string]any) {
	b, err := os.ReadFile("schema/http.schema.json")
	require.NoError(t, err)

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	require.NoError(t, c.AddResource(_schemaURL, doc))

	schema, err := c.Compile(_schemaURL)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))

	return schema, raw
}

// instance converts a document to what the JSON Schema validator takes.
func instance(t require.TestingT, doc any) any {
	b, err := json.Marshal(doc)
	require.NoError(t, err)

	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)

	return v
}

func TestJSONSchema_IDIsTheAddressTheDocsServe(t *testing.T) {
	// Mock files and editors name the schema by its $id: the address the docs site serves it at, which
	// docs/scripts/prepare.mjs copies it to. A schema whose $id names another address points them at nothing.
	_, raw := loadJSONSchema(t)

	require.Equal(t, "https://caffeine-projects.github.io/vlatte/schemas/http.schema.json", raw["$id"])
}

func TestJSONSchema_JSONMockFilesNameItInTheirSchemaKey(t *testing.T) {
	// A JSON mock file names its schema for editors in $schema, as a YAML one does in a comment: the schema must
	// accept the key, and the decoder must load the file as if the key weren't there.
	schema, _ := loadJSONSchema(t)
	doc := `{
		"$schema": "https://caffeine-projects.github.io/vlatte/schemas/http.schema.json",
		"request": {"method": "GET", "path": "/hello"},
		"response": {"status": 200, "body": "hello"}
	}`

	var v any
	require.NoError(t, json.Unmarshal([]byte(doc), &v))
	require.NoError(t, schema.Validate(instance(t, v)))

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)
	m.MustMock(FromBytes([]byte(doc), "json"))

	res, err := http.Get(m.URL("/hello"))
	require.NoError(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello", string(body))
}

func TestJSONSchema_KeysMatchTheTables(t *testing.T) {
	// Every key the decoder reads is documented for editors, and nothing else is.
	_, raw := loadJSONSchema(t)

	props := func(node any) map[string]any {
		obj, _ := node.(map[string]any)
		p, _ := obj["properties"].(map[string]any)

		return p
	}

	// keys lists the properties of an object schema, and those of the groups named, as dotted keys.
	keys := func(node any, groups ...string) []string {
		var out []string
		for k, v := range props(node) {
			out = append(out, k)
			if slices.Contains(groups, k) {
				for sub := range props(v) {
					out = append(out, k+"."+sub)
				}
			}
		}

		slices.Sort(out)

		return out
	}

	defs, _ := raw["$defs"].(map[string]any)

	// $schema is for editors: the decoder skips it.
	mockKeys := slices.DeleteFunc(keys(raw, "scenario", "request"), func(k string) bool { return k == "$schema" })
	require.Equal(t, _mockSchema.Keys(), mockKeys, "mock")
	require.Equal(t, _replySchema.Keys(), keys(defs["reply"], "template"), "reply")
	require.Equal(t, _sequenceSchema.Keys(), keys(props(raw)["response_sequence"]), "response_sequence")
	require.Equal(t, _randomSchema.Keys(), keys(props(raw)["response_random"]), "response_random")
	require.Equal(t, _proxySchema.Keys(), keys(props(raw)["proxy"]), "proxy")
	require.Equal(t, _eventSchema.Keys(), keys(defs["on_complete_event"]), "on_complete")
	require.Equal(t, _filePatternSchema.Keys(), keys(defs["file_pattern"]), "form_file")
	require.Equal(t, _basicAuthSchema.Keys(), keys(props(props(raw)["request"])["basic_auth"]), "basic_auth")
	require.Equal(t, mpart.SpecSchema.Keys(), keys(defs["multipart"]), "multipart")
	require.Equal(t, mpart.PartSchema.Keys(), keys(defs["multipart_part"]), "multipart part")
}

func TestPropJSONSchema_AgreesWithTheDecoder(t *testing.T) {
	// M11: the editor schema accepts every mock the decoder builds, and rejects the faults it can express.
	// The arguments of matchers are the decoder's alone.
	t.Parallel()

	schema, _ := loadJSONSchema(t)

	expressible := slices.DeleteFunc(slices.Clone(_mockFaults), schemaBlind)

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock("http://localhost:8080").Draw(t, "mock").doc()
		require.NoError(t, schema.Validate(instance(t, doc)), "a mock the decoder builds")

		// A proxy is a fault only next to another reply.
		if doc["response"] == nil && doc["response_sequence"] == nil && doc["response_random"] == nil {
			doc["response"] = map[string]any{"status": int64(200)}
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(expressible), 1, 3, func(f mockFault) string { return f.area }).Draw(t, "faults")
		for _, f := range faults {
			f.inject(doc)
		}

		require.Error(t, schema.Validate(instance(t, doc)), "faults %v", faults)
	})
}

func TestJSONSchema_AcceptsTheFixturesTheDecoderAccepts(t *testing.T) {
	// An editor must not mark a working mock file as wrong.
	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent().OnCompleteListener("webhook", stop))

	var files []string
	for _, pattern := range []string{"testdata/*/*.*", "testdata/*/*/*.*", "e2e/testdata/*/*.*", "../webhook/testdata/*.*"} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)

		files = append(files, matches...)
	}

	checked := 0

	for _, file := range files {
		format, ok := mockFileFormat(filepath.Ext(file))
		if !ok {
			continue
		}

		data, err := os.ReadFile(file)
		require.NoError(t, err)

		// dir, as FromFile sets it: without it, fixtures with relative files would be skipped as wrong.
		if _, err := (mockDoc{origin: file, format: format, dir: filepath.Dir(file), data: data}).build(m); err != nil {
			continue // wrong on purpose, or not a mock file
		}

		var doc any
		require.NoError(t, yaml.Unmarshal(data, &doc), file)
		require.NoError(t, schema.Validate(instance(t, doc)), file)

		checked++
	}

	require.Greater(t, checked, 20, "the fixtures are checked")
}

// _scalarKeys places a value at each key of the mock format that takes a scalar kind, in a document that is
// valid with the key's valid value.
var _scalarKeys = []struct {
	key   string
	valid any
	doc   func(v any) map[string]any
}{
	{"id", "users", topLevel("id")},
	{"name", "users", topLevel("name")},
	{"priority", int64(1), topLevel("priority")},
	{"enabled", true, topLevel("enabled")},
	{"delay", "1s", topLevel("delay")},
	{"repeat", int64(2), topLevel("repeat")},
	{"scenario.name", "s", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": v}})
	}},
	{"scenario.required_state", "a", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": "s", "required_state": v}})
	}},
	{"scenario.new_state", "b", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": "s", "new_state": v}})
	}},
	{"scenario.key", "k", func(v any) map[string]any {
		return withRequest(map[string]any{"scenario": map[string]any{"name": "s", "key": v}})
	}},
	{"request.url_match", "x", func(v any) map[string]any { return map[string]any{"request": map[string]any{"url_match": v}} }},
	{"request.path_template", "/users/{id}", func(v any) map[string]any {
		return map[string]any{"request": map[string]any{"path_template": v}}
	}},
	{"response.status", int64(200), inReply("status")},
	{"response.body_base64", "//4A\n//4A\n", inReply("body_base64")},
	{"response.body_file", "b.txt", inReply("body_file")},
	{"response.encoding", "gzip", inReply("encoding")},
	{"response.template.enabled", true, func(v any) map[string]any {
		return withRequest(map[string]any{"response": map[string]any{"template": map[string]any{"enabled": v}}})
	}},
	{"response_random.seed", int64(1), func(v any) map[string]any {
		return withRequest(map[string]any{"response_random": map[string]any{"responses": []any{make(map[string]any)}, "seed": v}})
	}},
	{"proxy.target", "http://a", func(v any) map[string]any { return withRequest(map[string]any{"proxy": map[string]any{"target": v}}) }},
	{"proxy.remove_request_headers", []any{"a"}, inProxy("remove_request_headers")},
	{"proxy.trim_prefix", "/a", inProxy("trim_prefix")},
	{"proxy.trim_suffix", "/b", inProxy("trim_suffix")},
	{"proxy.timeout", int64(10), inProxy("timeout")},
	{"proxy.ssl_verify", true, inProxy("ssl_verify")},
	{"proxy.x_forwarded", true, inProxy("x_forwarded")},
	{"proxy.preserve_host", true, inProxy("preserve_host")},
	{"on_complete.name", "hook", func(v any) map[string]any {
		return withRequest(map[string]any{"on_complete": []any{map[string]any{"name": v}}})
	}},
	{"on_complete.after_response", true, func(v any) map[string]any {
		return withRequest(map[string]any{"on_complete": []any{map[string]any{"name": "hook", "after_response": v}}})
	}},
	{"request.form_file.f.every", true, func(v any) map[string]any {
		return map[string]any{"request": map[string]any{"form_file": map[string]any{"f": map[string]any{"every": v}}}}
	}},
	{"response.multipart.type", "mixed", inMultipart(func(v any) map[string]any {
		return map[string]any{"type": v, "parts": []any{map[string]any{"body": "x"}}}
	})},
	{"response.multipart.boundary", "b0", inMultipart(func(v any) map[string]any {
		return map[string]any{"type": "mixed", "boundary": v, "parts": []any{map[string]any{"body": "x"}}}
	})},
	{"response.multipart.parts[0].name", "a", inPart("name")},
	{"response.multipart.parts[0].filename", "a.png", inPart("filename")},
	{"response.multipart.parts[0].content_type", "image/png", inPart("content_type")},
	{"response.multipart.parts[0].body_base64", "//4A\n//4A\n", inPart("body_base64")},
	{"response.multipart.parts[0].body_file", "a.png", inPart("body_file")},
	{"response.multipart.parts[0].delay", "1s", inPart("delay")},
}

func inMultipart(multipart func(v any) map[string]any) func(v any) map[string]any {
	return func(v any) map[string]any {
		return withRequest(map[string]any{"response": map[string]any{"multipart": multipart(v)}})
	}
}

func inPart(key string) func(v any) map[string]any {
	return inMultipart(func(v any) map[string]any {
		return map[string]any{"type": "mixed", "parts": []any{map[string]any{key: v}}}
	})
}

func withRequest(doc map[string]any) map[string]any {
	doc["request"] = map[string]any{"path": "/x"}
	return doc
}

func topLevel(key string) func(v any) map[string]any {
	return func(v any) map[string]any { return withRequest(map[string]any{key: v}) }
}

func inReply(key string) func(v any) map[string]any {
	return func(v any) map[string]any { return withRequest(map[string]any{"response": map[string]any{key: v}}) }
}

func inProxy(key string) func(v any) map[string]any {
	return func(v any) map[string]any {
		return withRequest(map[string]any{"proxy": map[string]any{"target": "http://a", key: v}})
	}
}

// _substitutes are values of every class, and text that reads as other kinds, put in place of a valid value.
var _substitutes = []any{
	nil, true, "true", "t", "yes", int64(0), int64(1), int64(5), "5", 1.5, "1.5s",
	[]any{},
	[]any{"a"},
	[]any{int64(1)},
	make(map[string]any),
}

func TestJSONSchema_ScalarTypesAgreeWithTheDecoder(t *testing.T) {
	// M16: for every key that takes a scalar kind, the editor schema accepts a value exactly when the decoder does,
	// so an editor never marks a working mock file as wrong, nor misses a value the decoder rejects (Codex review:
	// the decoder took name: 123, which editors reject).
	schema, _ := loadJSONSchema(t)

	setup := Setup().Silent().OnCompleteListener("hook", stop)
	for _, v := range _substitutes {
		if s, ok := v.(string); ok {
			setup.OnCompleteListener(s, stop)
		}
	}

	m := MustNew(setup)

	covered := make([]string, 0, len(_scalarKeys))
	for _, row := range _scalarKeys {
		covered = append(covered, row.key)

		for i, v := range append([]any{row.valid}, _substitutes...) {
			doc := row.doc(v)

			data, err := config.Encode(doc, "json")
			require.NoError(t, err)

			_, decodeErr := mockDoc{origin: "doc", format: "json", data: data}.build(m)
			schemaErr := schema.Validate(instance(t, doc))

			require.Equal(t, decodeErr == nil, schemaErr == nil, "%s: %#v\ndecoder: %v\nschema: %v", row.key, v, decodeErr, schemaErr)

			if i == 0 {
				require.NoError(t, decodeErr, "%s takes %#v", row.key, v)
			}
		}
	}

	// Every key of a scalar kind has a row.
	var scalar []string
	for prefix, keys := range map[string]func() ([]string, func(string) (config.Kind, bool)){
		"":                 func() ([]string, func(string) (config.Kind, bool)) { return _mockSchema.Keys(), _mockSchema.KindOf },
		"response.":        func() ([]string, func(string) (config.Kind, bool)) { return _replySchema.Keys(), _replySchema.KindOf },
		"response_random.": func() ([]string, func(string) (config.Kind, bool)) { return _randomSchema.Keys(), _randomSchema.KindOf },
		"response_sequence.": func() ([]string, func(string) (config.Kind, bool)) {
			return _sequenceSchema.Keys(), _sequenceSchema.KindOf
		},
		"proxy.":       func() ([]string, func(string) (config.Kind, bool)) { return _proxySchema.Keys(), _proxySchema.KindOf },
		"on_complete.": func() ([]string, func(string) (config.Kind, bool)) { return _eventSchema.Keys(), _eventSchema.KindOf },
		"request.form_file.f.": func() ([]string, func(string) (config.Kind, bool)) {
			return _filePatternSchema.Keys(), _filePatternSchema.KindOf
		},
		"response.multipart.": func() ([]string, func(string) (config.Kind, bool)) {
			return mpart.SpecSchema.Keys(), mpart.SpecSchema.KindOf
		},
		"response.multipart.parts[0].": func() ([]string, func(string) (config.Kind, bool)) {
			return mpart.PartSchema.Keys(), mpart.PartSchema.KindOf
		},
	} {
		all, kindOf := keys()
		for _, key := range all {
			// A pattern's body_file must name a file that exists, which no JSON Schema can check: the rule table
			// pins its type, and the fault catalogue its file_not_found.
			if prefix+key == "request.form_file.f.body_file" {
				continue
			}

			if kind, ok := kindOf(key); ok && kind != config.Any {
				scalar = append(scalar, prefix+key)
			}
		}
	}

	require.ElementsMatch(t, scalar, covered)
}

func TestPropJSONSchema_DurationPattern(t *testing.T) {
	// M17: the editor's duration pattern, the decoder and time.ParseDuration agree on every string:
	// it is an unsigned Go duration, or it is wrong for all three.
	t.Parallel()

	_, raw := loadJSONSchema(t)

	defs, _ := raw["$defs"].(map[string]any)
	duration, _ := defs["duration"].(map[string]any)
	forms, _ := duration["oneOf"].([]any)
	text, _ := forms[1].(map[string]any)
	source, _ := text["pattern"].(string)
	pattern := regexp.MustCompile(source)

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		s := genDurationText().Draw(t, "duration")

		_, err := time.ParseDuration(s)
		want := err == nil && !strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "-")

		require.Equal(t, want, pattern.MatchString(s), "pattern on %q", s)

		data, err := config.Encode(withRequest(map[string]any{"delay": s}), "json")
		require.NoError(t, err)

		_, err = mockDoc{origin: "doc", format: "json", data: data}.build(m)
		require.Equal(t, want, err == nil, "decoder on %q: %v", s, err)
	})
}

func TestJSONSchema_ProxyTargetPattern(t *testing.T) {
	// #63: the editor's pattern for proxy.target took any scheme, which the decoder refuses. Both take an absolute http
	// or https URL only, in any case.
	_, raw := loadJSONSchema(t)

	props, _ := raw["properties"].(map[string]any)
	proxy, _ := props["proxy"].(map[string]any)
	proxyProps, _ := proxy["properties"].(map[string]any)
	target, _ := proxyProps["target"].(map[string]any)
	source, _ := target["pattern"].(string)
	require.NotEmpty(t, source, "proxy.target has no pattern")

	pattern := regexp.MustCompile(source)

	for target, want := range map[string]bool{
		"http://api.local:8080": true,
		"https://api.local/v1":  true,
		"HTTPS://api.local":     true,
		"ftp://files.local":     false,
		"ws://api.local":        false,
		"/x":                    false,
		"https://":              false,
		"mailto:a@b":            false,
	} {
		_, err := checkTargetURL(target)
		require.Equal(t, want, err == nil, "decoder on %q: %v", target, err)
		require.Equal(t, want, pattern.MatchString(target), "pattern on %q", target)
	}
}

// faultSpec returns the spec of a fault of type typ, with the keys and values of its parameters.
func faultSpec(typ string, params ...any) map[string]any {
	spec := map[string]any{"type": typ}
	for i := 0; i < len(params); i += 2 {
		spec[params[i].(string)] = params[i+1]
	}

	return spec
}

func TestJSONSchema_FaultsAgreeWithTheDecoder(t *testing.T) {
	// The editor schema accepts a fault spec exactly when the decoder builds it, at each level. A slicer's variation
	// must stay below its size, and a latency's jitter within it, which the schema can't compare: the decoder alone
	// checks them.
	schema, _ := loadJSONSchema(t)
	m := MustNew(Setup().Silent())
	t.Cleanup(m.Close)

	tests := []struct {
		spec  any
		valid bool
	}{
		{faultSpec("empty_response"), true},
		{faultSpec("malformed_response_chunk"), true},
		{faultSpec("random_data_then_close"), true},
		{faultSpec("connection_reset_by_peer"), true},
		{faultSpec("connection_reset_by_peer_after", "after", "1s"), true},
		{faultSpec("chunked_dribble_delay", "chunks", int64(3), "total", int64(300)), true},
		{faultSpec("latency", "latency", "100ms"), true},
		{faultSpec("latency", "latency", "100ms", "jitter", "20ms"), true},
		{faultSpec("bandwidth", "rate", int64(1)), true},
		{faultSpec("bandwidth", "rate", "good_3g"), true},
		{faultSpec("slicer", "average_size", int64(8)), true},
		{faultSpec("slicer", "average_size", int64(8), "size_variation", int64(7), "delay", "0"), true},
		{faultSpec("slow_close", "delay", int64(0)), true},
		{faultSpec("timeout"), true},
		{faultSpec("timeout", "after", "1.5s"), true},
		{faultSpec("limit_data", "bytes", int64(0)), true},
		{"empty_response", false},
		{[]any{"slow_close", "1s"}, false},
		{map[string]any{"latency": "1s"}, false},
		{faultSpec("nope"), false},
		{faultSpec("Latency", "latency", "1s"), false},
		{faultSpec("slow_close"), false},
		{faultSpec("empty_response", "delay", "1s"), false},
		{faultSpec("latency", "latency", "1s", "latncy", "1s"), false},
		{faultSpec("connection_reset_by_peer_after", "after", "-1s"), false},
		{faultSpec("chunked_dribble_delay", "chunks", int64(0), "total", "1s"), false},
		{faultSpec("chunked_dribble_delay", "chunks", int64(3)), false},
		{faultSpec("latency", "latency", "1s", "jitter", int64(-1)), false},
		{faultSpec("latency", "latency", "1s", "jitter", 12.5), false},
		{faultSpec("bandwidth", "rate", int64(0)), false},
		{faultSpec("bandwidth", "rate", "5g"), false},
		{faultSpec("slicer", "average_size", int64(0)), false},
		{faultSpec("slicer", "average_size", int64(4), "size_variation", int64(-1)), false},
		{faultSpec("timeout", "after", "1500"), false},
		{faultSpec("limit_data", "bytes", 1.5), false},
	}

	for _, key := range []string{"steps", "wire_steps", "upstream_steps"} {
		for _, tc := range tests {
			doc := withRequest(map[string]any{key: []any{tc.spec}})

			data, err := config.Encode(doc, "json")
			require.NoError(t, err)

			_, decodeErr := mockDoc{origin: "doc", format: "json", data: data}.build(m)
			require.Equal(t, tc.valid, decodeErr == nil, "%s: %#v: %v", key, tc.spec, decodeErr)
			require.Equal(t, tc.valid, schema.Validate(instance(t, doc)) == nil, "%s: %#v", key, tc.spec)
		}
	}
}

// docBuilder builds a mock from a document, without the template pass, as the admin API does.
type docBuilder struct {
	doc mockDoc
}

func (b docBuilder) Build(app *Server) (*Mock, error) {
	return b.doc.build(app)
}

// send sends a request and reads its whole response.
func send(t require.TestingT, base string, req requestSpec) (*http.Response, []byte) {
	u := base + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}

	var body io.Reader

	contentType := ""

	switch {
	case req.multipart:
		form := formModel{files: req.files}
		for _, k := range slices.Sorted(maps.Keys(req.form)) {
			for _, v := range req.form[k] {
				form.fields = append(form.fields, formField{name: k, value: v})
			}
		}

		var b []byte
		contentType, b = form.encode(t)
		body = bytes.NewReader(b)
	case req.form != nil:
		body = strings.NewReader(req.form.Encode())
		contentType = MIMEFormURLEncoded
	case req.body != nil:
		body = strings.NewReader(*req.body)
		contentType = MIMETextPlain
	}

	r, err := http.NewRequest(req.method, u, body)
	require.NoError(t, err)

	for k, vv := range req.header {
		for _, v := range vv {
			r.Header.Add(k, v)
		}
	}

	if contentType != "" {
		r.Header.Set(HeaderContentType, contentType)
	}

	res, err := http.DefaultClient.Do(r)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

// replyDiff says how a response differs from what a reply model predicts, or "" when it doesn't.
func replyDiff(want replyModel, method string, res *http.Response, body []byte) string {
	if res.StatusCode != want.status {
		return fmt.Sprintf("status %d, want %d", res.StatusCode, want.status)
	}

	byName := make(map[string][]string)
	for _, h := range want.header {
		byName[h.name] = append(byName[h.name], h.value)
	}

	for name, values := range byName {
		if got := res.Header.Values(name); !slicesEqual(got, values) {
			return fmt.Sprintf("header %s %q, want %q", name, got, values)
		}
	}

	for _, name := range want.template {
		if got := res.Header.Get(name); got != method {
			return fmt.Sprintf("templated header %s %q, want the method %q", name, got, method)
		}
	}

	for _, h := range want.trailer {
		if got := res.Trailer.Get(h.name); got != h.value {
			return fmt.Sprintf("trailer %s %q, want %q", h.name, got, h.value)
		}
	}

	switch {
	case want.multipart != nil:
		mediaType, _, err := mime.ParseMediaType(res.Header.Get(HeaderContentType))
		if err != nil || mediaType != "multipart/"+strings.ToLower(want.multipart.subtype) {
			return fmt.Sprintf("content type %q, want multipart/%s", res.Header.Get(HeaderContentType), want.multipart.subtype)
		}

		if got := readMultipartParts(res.Header.Get(HeaderContentType), body); !reflect.DeepEqual(got, want.multipart.want()) {
			return fmt.Sprintf("multipart parts %v, want %v", got, want.multipart.want())
		}
	case want.text != nil:
		if string(body) != *want.text {
			return fmt.Sprintf("body %q, want %q", body, *want.text)
		}
	case want.data != nil:
		if string(body) != *want.data {
			return fmt.Sprintf("templated body %q, want %q", body, *want.data)
		}
	case want.hasJSON:
		// The body is the value as deterministic JSON: same value, same text.
		wantJSON, err := json.Marshal(want.json, json.Deterministic(true))
		if err != nil {
			return err.Error()
		}

		if string(body) != string(wantJSON) {
			return fmt.Sprintf("JSON body %s, want %s", body, wantJSON)
		}
	default:
		if len(body) != 0 {
			return fmt.Sprintf("body %q, want none", body)
		}
	}

	return ""
}

func slicesEqual(a, b []string) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func TestPropMockFile_ServesItsModel(t *testing.T) {
	// M2: a mock file means what it says. A request built to match gets the modeled replies, in order and
	// within repeat; a request that differs in any one matched part gets the no-match status.
	// M18: that holds for multipart forms too: their fields, and the filename, content type and content of a file,
	// whatever boundary the client picks.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		model := genMock(m.URL()).Draw(t, "mock")
		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "model", format: format, data: encodeMock(t, model.doc(), format)}})
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		// Mismatches first: they must not count towards repeat.
		for _, req := range model.request.mismatches(t) {
			res, _ := send(t, m.URL(), req)
			require.Equal(t, StatusNoMatch, res.StatusCode, "a request with another %s must not match", req.what)
		}

		require.Zero(t, scope.Hits())

		if !model.enabled {
			res, _ := send(t, m.URL(), model.request.matching(t))
			require.Equal(t, StatusNoMatch, res.StatusCode, "a disabled mock must not match")

			return
		}

		served := model.served()
		for i, want := range served {
			req := model.request.matching(t)
			start := time.Now()
			res, body := send(t, m.URL(), req)

			if i == 0 {
				require.GreaterOrEqual(t, time.Since(start), model.delay, "delay")
			}

			if model.random != nil {
				diffs := make([]string, 0, len(model.random))
				for _, r := range model.random {
					diffs = append(diffs, replyDiff(r, req.method, res, body))
				}

				require.Contains(t, diffs, "", "random reply %d is none of the replies: %v", i, diffs)

				continue
			}

			require.Empty(t, replyDiff(want, req.method, res, body), "reply %d of %d", i+1, len(served))
		}

		if model.repeat > 0 && int64(len(served)) == model.repeat {
			res, _ := send(t, m.URL(), model.request.matching(t))
			require.Equal(t, StatusNoMatch, res.StatusCode, "the mock was served %d times, as many as repeat", model.repeat)
		}
	})
}

func TestPropMockFile_FormatsBuildTheSameMock(t *testing.T) {
	// M1: the same mock written as JSON and as YAML builds the same mock.
	t.Parallel()

	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock(m.URL()).Draw(t, "mock").doc()
		if _, ok := doc["id"]; !ok {
			doc["id"] = "same" // generated IDs differ; the rest must not
		}

		described := make([]string, 0, len(MockFileFormats()))
		for _, format := range MockFileFormats() {
			mock, err := mockDoc{origin: format, format: format, data: encodeMock(t, doc, format)}.build(m)
			require.NoError(t, err, format)

			described = append(described, describe(t, mock))
		}

		require.Equal(t, described[0], described[1], "json and yaml")
		require.Equal(t, described[0], described[2], "json and yml")
	})
}

func TestPropMockFile_BuildIsRepeatable(t *testing.T) {
	// M5: a builder builds a new mock each time, equal to the last, with its own state.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock("http://localhost").Draw(t, "mock").doc()
		doc["id"] = "same"

		b := docBuilder{mockDoc{origin: "doc", format: "json", data: encodeMock(t, doc, "json")}}

		first, err := b.Build(m)
		require.NoError(t, err)
		second, err := b.Build(m)
		require.NoError(t, err)

		require.NotSame(t, first, second)
		require.Equal(t, describe(t, first), describe(t, second))

		first.Inc()
		require.Zero(t, second.Hits())
	})
}

func TestPropMockFile_DurationsAreTheSameEverywhere(t *testing.T) {
	// M6: delay and proxy.timeout read the same value the same way: integers are milliseconds (C2).
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		var raw any = rapid.Int64Range(0, 1<<40).Draw(t, "ms")
		if rapid.Bool().Draw(t, "as a Go duration") {
			raw = time.Duration(rapid.Int64Range(0, 1<<50).Draw(t, "ns")).String()
		}

		doc := map[string]any{
			"request": map[string]any{"path": "/x"},
			"delay":   raw,
			"proxy":   map[string]any{"target": "http://localhost", "timeout": raw},
		}

		def := newMockDef(_builtinMatchers)
		_, err := _mockSchema.Load(&def, []config.Source{config.Bytes("doc", "yaml", encodeMock(t, doc, "yaml"))})
		require.NoError(t, err)

		require.Equal(t, def.delay, def.proxy.timeout)

		if ms, ok := raw.(int64); ok {
			require.Equal(t, time.Duration(ms)*time.Millisecond, def.delay)
		}
	})
}

func TestPropMockFile_IDRule(t *testing.T) {
	// M7: a file's id is accepted iff it follows the rule, and becomes the mock's ID exactly.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		id := rapid.OneOf(rapid.StringMatching(`[A-Za-z0-9._~-]{1,12}`), rapid.String()).Draw(t, "id")
		doc := map[string]any{"id": id, "request": map[string]any{"path": "/x"}}

		mock, err := mockDoc{origin: "doc", format: "json", data: encodeMock(t, doc, "json")}.build(m)
		if _idOracle.MatchString(id) {
			require.NoError(t, err)
			require.Equal(t, id, mock.ID())

			return
		}

		problems := mockProblems(t, err)
		require.Len(t, problems, 1)
		require.Equal(t, "id", problems[0].Key)
	})
}

var errListener = errors.New("listener stopped")

// stop is a listener that never runs: a mock needs its on_complete listeners registered.
var stop = vlatte.EventFunc[vlatte.OnComplete](func(context.Context, vlatte.OnComplete) error { return errListener })

// readMultipartParts reads the parts of a multipart body as a client does, or nil when it doesn't read.
func readMultipartParts(contentType string, body []byte) []readPart {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil
	}

	var parts []readPart

	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := r.NextRawPart()
		if errors.Is(err, io.EOF) {
			return parts
		}

		if err != nil {
			return nil
		}

		b, err := io.ReadAll(part)
		if err != nil {
			return nil
		}

		parts = append(parts, readPart{header: part.Header, body: string(b)})
	}
}

// mockFault is an invalid setting injected into a valid mock document, and the problem it must cause.
type mockFault struct {
	area   string // what it touches, so two faults never touch the same thing
	code   string
	key    string
	inject func(doc map[string]any)
}

// _mockFaults touch distinct settings of the base document of genFaulty, field by field.
var _mockFaults = []mockFault{
	{"unknown", config.CodeUnknownKey, "zzz", func(doc map[string]any) { doc["zzz"] = int64(1) }},
	{"status", config.CodeInvalidValue, "response.status", func(doc map[string]any) { sub(doc, "response")["status"] = "abc" }},
	{"typo", config.CodeUnknownKey, "response.stauts", func(doc map[string]any) { sub(doc, "response")["stauts"] = int64(201) }},
	{"encoding", config.CodeInvalidValue, "response.encoding", func(doc map[string]any) { sub(doc, "response")["encoding"] = "br" }},
	{"header", config.CodeInvalidValue, "response.header.X-Count", func(doc map[string]any) {
		sub(doc, "response")["header"] = map[string]any{"X-Count": int64(10)}
	}},
	{"method", config.CodeInvalidValue, "request.method", func(doc map[string]any) { sub(doc, "request")["method"] = int64(5) }},
	{"url_match", config.CodeInvalidValue, "request.url_match", func(doc map[string]any) {
		r := sub(doc, "request")
		delete(r, "path")
		r["url_match"] = "("
	}},
	{"path_params", config.CodeMissingValue, "request.path_params", func(doc map[string]any) {
		sub(doc, "request")["path_params"] = map[string]any{"id": "7"}
	}},
	{"query", config.CodeInvalidValue, "request.query.page", func(doc map[string]any) {
		sub(doc, "request")["query"] = map[string]any{"page": int64(1)}
	}},
	{"matcher", config.CodeInvalidValue, "request.header.X-Id", func(doc map[string]any) {
		sub(doc, "request")["header"] = map[string]any{"X-Id": []any{"gt"}}
	}},
	{"headers", config.CodeInvalidValue, "request.headers.X-Tag", func(doc map[string]any) {
		sub(doc, "request")["headers"] = map[string]any{"X-Tag": "a"}
	}},
	{"queries", config.CodeInvalidValue, "request.queries.page", func(doc map[string]any) {
		sub(doc, "request")["queries"] = map[string]any{"page": "2"}
	}},
	{"basic_auth", config.CodeMissingValue, "request.basic_auth.password", func(doc map[string]any) {
		sub(doc, "request")["basic_auth"] = map[string]any{"username": "u"}
	}},
	{"delay", config.CodeInvalidValue, "delay", func(doc map[string]any) { doc["delay"] = int64(-5) }},
	{"repeat", config.CodeInvalidValue, "repeat", func(doc map[string]any) { doc["repeat"] = int64(0) }},
	{"id", config.CodeInvalidValue, "id", func(doc map[string]any) { doc["id"] = "a/b" }},
	{"proxy", config.CodeConflict, "proxy", func(doc map[string]any) { doc["proxy"] = map[string]any{"target": "http://localhost"} }},
	{"on_complete", config.CodeInvalidValue, "on_complete", func(doc map[string]any) { doc["on_complete"] = map[string]any{"name": "x"} }},
	{"scenario", config.CodeMissingValue, "scenario.name", func(doc map[string]any) {
		doc["scenario"] = map[string]any{"required_state": "a"}
	}},
	{"ext", config.CodeInvalidValue, "ext", func(doc map[string]any) { doc["ext"] = []any{int64(1)} }},
	{"name", config.CodeInvalidValue, "name", func(doc map[string]any) { doc["name"] = int64(123) }},
	{"priority", config.CodeInvalidValue, "priority", func(doc map[string]any) { doc["priority"] = "5" }},
	{"enabled", config.CodeInvalidValue, "enabled", func(doc map[string]any) { doc["enabled"] = "yes" }},
	// The form_file faults share an area: one sets the whole form_file, which would hide the others.
	{"form_file", config.CodeInvalidValue, "request.form_file", func(doc map[string]any) { sub(doc, "request")["form_file"] = []any{} }},
	{"form_file", config.CodeUnknownKey, "request.form_file.avatar.filname", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"filname": "a.png"}}
	}},
	{"form_file", config.CodeConflict, "request.form_file.avatar.body_file", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"body": "x", "body_file": "a.png"}}
	}},
	{"form_file", config.CodeFileNotFound, "request.form_file.avatar.body_file", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": map[string]any{"body_file": "testdata/no-such-file.png"}}
	}},
	{"form_file", config.CodeInvalidValue, "request.form_file.avatar[1].every", func(doc map[string]any) {
		sub(doc, "request")["form_file"] = map[string]any{"avatar": []any{make(map[string]any), map[string]any{"every": "x"}}}
	}},
	// The multipart faults share an area: each sets response.multipart and drops the body it would conflict with.
	{"multipart", config.CodeInvalidValue, "response.multipart.type", func(doc map[string]any) { withMultipart(doc)["type"] = "multipart/mixed" }},
	{"multipart", config.CodeMissingValue, "response.multipart.type", func(doc map[string]any) { delete(withMultipart(doc), "type") }},
	{"multipart", config.CodeInvalidValue, "response.multipart.boundary", func(doc map[string]any) { withMultipart(doc)["boundary"] = "a b " }},
	{"multipart", config.CodeNotAllowed, "response.multipart.params.boundary", func(doc map[string]any) {
		withMultipart(doc)["params"] = map[string]any{"boundary": "x"}
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts", func(doc map[string]any) { withMultipart(doc)["parts"] = []any{} }},
	// Subtypes ignore case, in the decoder and in the editor schema.
	{"multipart", config.CodeMissingValue, "response.multipart.parts[0].name", func(doc map[string]any) { withMultipart(doc)["type"] = "Form-Data" }},
	{"multipart", config.CodeUnknownKey, "response.multipart.parts[0].nmae", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"nmae": "x"}}
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts[0].delay", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"delay": int64(-5)}}
	}},
	{"multipart", config.CodeConflict, "response.multipart.parts[0].body_base64", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"body": "x", "body_base64": "eA=="}}
	}},
	{"multipart", config.CodeConflict, "response.multipart.parts[0].body_file", func(doc map[string]any) {
		withMultipart(doc)["parts"] = []any{map[string]any{"body_base64": "eA==", "body_file": "x.bin"}}
	}},
	{"multipart", config.CodeConflict, "response.multipart", func(doc map[string]any) {
		withMultipart(doc)
		sub(doc, "response")["body"] = "x"
	}},
	{"multipart", config.CodeInvalidValue, "response.multipart.parts[0].body", func(doc map[string]any) {
		multipart := withMultipart(doc)
		multipart["boundary"] = "b"
		multipart["parts"] = []any{map[string]any{"body": "a\n--b"}}
	}},
}

// withMultipart replaces the body of the response with a multipart body, and returns the multipart object.
func withMultipart(doc map[string]any) map[string]any {
	response := sub(doc, "response")
	delete(response, "body")

	multipart := map[string]any{"type": "mixed", "parts": []any{map[string]any{"body": "x"}}}
	response["multipart"] = multipart

	return multipart
}

// schemaBlind reports the faults the editor schema can't express: matcher arguments are the decoder's alone, and no
// JSON Schema can tell whether a file exists, or whether a line of a body starts with the boundary.
func schemaBlind(f mockFault) bool {
	return f.area == "matcher" || f.code == config.CodeFileNotFound || f.key == "response.multipart.parts[0].body"
}

// sub returns the object under key, creating it.
func sub(doc map[string]any, key string) map[string]any {
	obj, ok := doc[key].(map[string]any)
	if !ok {
		obj = make(map[string]any)
		doc[key] = obj
	}

	return obj
}

func TestPropMockFile_EveryFaultIsReported(t *testing.T) {
	// M3: each fault is reported once, with its code and key, and nothing else is: a file is fixed in one pass.
	t.Parallel()

	m := MustNew(Setup().Silent())

	rapid.Check(t, func(t *rapid.T) {
		doc := map[string]any{
			"request":  map[string]any{"method": "GET", "path": "/" + genToken().Draw(t, "path")},
			"response": map[string]any{"status": int64(200), "body": genToken().Draw(t, "body")},
		}

		faults := rapid.SliceOfNDistinct(rapid.SampledFrom(_mockFaults), 1, len(_mockFaults),
			func(f mockFault) string { return f.area }).Draw(t, "faults")

		want := make([]string, 0, len(faults))
		for _, f := range faults {
			f.inject(doc)
			want = append(want, f.code+" "+f.key)
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		_, err := mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}.build(m)

		got := make([]string, 0, len(want))
		for _, p := range mockProblems(t, err) {
			require.Equal(t, "doc", p.Origin)
			got = append(got, p.Code+" "+p.Key)
		}

		require.ElementsMatch(t, want, got)
	})
}

// mutate changes one random place of a document: it deletes, renames, retypes, wraps or adds a key.
func mutate(t *rapid.T, doc map[string]any) {
	// Collect the objects of the document and pick one to change.
	var objects []map[string]any

	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			objects = append(objects, x)
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)

	obj := rapid.SampledFrom(objects).Draw(t, "object")
	keys := slices.Sorted(maps.Keys(obj))

	op := rapid.IntRange(0, 4).Draw(t, "mutation")
	if len(keys) == 0 {
		op = 4
	}

	switch op {
	case 0:
		delete(obj, rapid.SampledFrom(keys).Draw(t, "deleted"))
	case 1:
		k := rapid.SampledFrom(keys).Draw(t, "renamed")
		obj[k+rapid.SampledFrom([]string{"x", "_", "s"}).Draw(t, "suffix")] = obj[k]
		delete(obj, k)
	case 2:
		obj[rapid.SampledFrom(keys).Draw(t, "retyped")] = genAny(2).Draw(t, "new value")
	case 3:
		k := rapid.SampledFrom(keys).Draw(t, "wrapped")
		obj[k] = []any{obj[k]}
	default:
		obj[rapid.SampledFrom([]string{"status", "body", "header", "path", "method", "query", "x", "responses", "target", "data"}).Draw(t, "added")] = genAny(2).Draw(t, "added value")
	}
}

// logBuffer collects the server's logs, safe for concurrent writes.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestPropMockFile_MutationsNeverPanic(t *testing.T) {
	// M4: whatever a document turns into, building it gives a mock or a list of problems, never a panic;
	// and a mock built from it never panics serving a request.
	t.Parallel()

	logs := &logBuffer{}
	logger := zerolog.New(logs)

	m := MustNew(Setup().Logger(&logger).OnCompleteListener("x", stop))
	m.MustStart()
	t.Cleanup(m.Close)

	var built, failed int

	rapid.Check(t, func(t *rapid.T) {
		doc := genMock(m.URL()).Draw(t, "mock").doc()
		for range rapid.IntRange(1, 4).Draw(t, "mutations") {
			mutate(t, doc)
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}})
		if err != nil {
			_, ok := errors.AsType[*config.Error](err)
			require.True(t, ok, "a mock document fails with its problems, not %v", err)

			failed++

			return
		}

		built++

		defer scope.Clear(context.Background())

		req := requestSpec{
			method: rapid.SampledFrom(_genMethods).Draw(t, "method"),
			path:   rapid.SampledFrom([]string{"/", "/x", "/" + genToken().Draw(t, "path")}).Draw(t, "path"),
			header: http.Header{"X-Id": {genToken().Draw(t, "header")}},
		}

		if req.method == http.MethodPost {
			body := rapid.SampledFrom([]string{"", "x", `{"a": 1}`}).Draw(t, "body")
			req.body = &body
		}

		send(t, m.URL(), req)

		require.NotContains(t, logs.String(), "panic", "serving %v", doc)
	})

	t.Logf("built %d mocks, rejected %d documents", built, failed)
	require.Positive(t, built, "the property must serve built mocks, not only reject documents")
	require.Positive(t, failed, "the property must reject broken documents, not only build them")
}

func FuzzMockDoc(f *testing.F) {
	// M14: no document panics the builder; every failure is a mock *config.Error.
	seeds, err := filepath.Glob("testdata/*/*.*")
	require.NoError(f, err)

	more, err := filepath.Glob("e2e/testdata/*/*.*")
	require.NoError(f, err)

	for _, path := range append(seeds, more...) {
		format, ok := mockFileFormat(filepath.Ext(path))
		if !ok {
			continue
		}

		data, err := os.ReadFile(path)
		require.NoError(f, err)

		f.Add(data, format)
	}

	m := MustNew(Setup().Silent().OnCompleteListener("webhook", stop))

	f.Fuzz(func(t *testing.T, data []byte, format string) {
		_, err := mockDoc{origin: "fuzz", format: format, data: data}.build(m)
		if err == nil {
			return
		}

		cfgErr, ok := errors.AsType[*config.Error](err)
		require.True(t, ok, "want a *config.Error, got %v", err)
		require.Equal(t, "mock", cfgErr.Scope)

		for _, p := range cfgErr.Problems {
			require.NotEmpty(t, p.Code)
			require.NotEmpty(t, p.Origin)
			require.NotEmpty(t, p.Message)
		}
	})
}

func TestPropMockFile_KeysKeepTheirText(t *testing.T) {
	// M8 (B6): keys reach templates, ext handlers and listeners exactly as written.
	t.Parallel()

	handler := &capturingHandler{}
	params := make(chan any, 1)
	capture := vlatte.EventFunc[vlatte.OnComplete](func(_ context.Context, args vlatte.OnComplete) error {
		params <- args.Parameters()
		return nil
	})

	m := MustNew(Setup().Silent().MockFileHandlers(handler).OnCompleteListener("capture", capture))
	m.MustStart()
	t.Cleanup(m.Close)

	rapid.Check(t, func(t *rapid.T) {
		key := genKey().Draw(t, "key")
		value := genToken().Draw(t, "value")
		reqPath := "/keys/" + genToken().Draw(t, "path")

		doc := map[string]any{
			"request": map[string]any{"path": reqPath},
			"response": map[string]any{
				"body":     "{{ index .Extra " + strconv.Quote(key) + " }}",
				"template": map[string]any{"enabled": true, "data": map[string]any{key: value}},
			},
			"ext":         map[string]any{key: value},
			"on_complete": []any{map[string]any{"name": "capture", "parameters": map[string]any{key: value}}},
		}

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")

		scope, err := m.Mock(docBuilder{mockDoc{origin: "doc", format: format, data: encodeMock(t, doc, format)}})
		require.NoError(t, err)

		defer scope.Clear(context.Background())

		require.Equal(t, map[string]any{key: value}, handler.got, "ext")

		res, body := send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, value, string(body), "template.data")

		select {
		case got := <-params:
			require.Equal(t, map[string]any{key: value}, got, "on_complete parameters")
		case <-time.After(5 * time.Second):
			t.Fatalf("the listener didn't run")
		}
	})
}

func TestPropMockFile_BodyFileIsRelativeToTheMockFile(t *testing.T) {
	// M10 (F17): a relative body_file opens from the mock file's directory, wherever the server runs;
	// a document without a file opens it from the working directory.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	files := t.TempDir()
	t.Chdir(t.TempDir())

	wd, err := os.Getwd()
	require.NoError(t, err)

	rapid.Check(t, func(t *rapid.T) {
		dir, err := os.MkdirTemp(files, "mocks")
		require.NoError(t, err)

		segments := rapid.SliceOfN(rapid.StringMatching(`[a-z0-9_-]{1,6}`), 1, 3).Draw(t, "body file")
		rel := path.Join(segments...) + ".txt"
		content := genToken().Draw(t, "content")
		reqPath := "/files/" + genToken().Draw(t, "path")

		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)

		format := rapid.SampledFrom(MockFileFormats()).Draw(t, "format")
		doc := map[string]any{"request": map[string]any{"path": reqPath}, "response": map[string]any{"body_file": rel}}
		mockFile := filepath.Join(dir, "body.mock."+format)
		writeFile(t, mockFile, string(encodeMock(t, doc, format)))

		scope, err := m.Mock(FromFile(mockFile))
		require.NoError(t, err)

		_, body := send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, content, string(body), "from the mock file's directory")
		require.NoError(t, scope.Clear(context.Background()))

		writeFile(t, filepath.Join(wd, filepath.FromSlash(rel)), content+"~")

		scope, err = m.Mock(FromBytes(encodeMock(t, doc, format), format))
		require.NoError(t, err)

		_, body = send(t, m.URL(), requestSpec{method: http.MethodGet, path: reqPath})
		require.Equal(t, content+"~", string(body), "from the working directory")
		require.NoError(t, scope.Clear(context.Background()))
	})
}

func writeFile(t require.TestingT, name, content string) {
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o700))
	require.NoError(t, os.WriteFile(name, []byte(content), 0o600))
}

func TestPropAdmin_AddsMocksByID(t *testing.T) {
	// M15: a mock posted to the admin API is stored under its id and served; posting the id again is a conflict.
	t.Parallel()

	m := MustNew(Setup().Silent().Admin(admin.MustNew()))
	m.MustStart()
	t.Cleanup(m.Close)

	seq := 0

	rapid.Check(t, func(t *rapid.T) {
		model := genMock(m.URL()).Draw(t, "mock")
		model.enabled, model.repeat = true, 0

		seq++
		model.id = "admin-" + strconv.Itoa(seq)

		body, err := json.Marshal(model.doc(), json.Deterministic(true))
		require.NoError(t, err)

		post := func() int {
			res, err := http.Post(m.AdminURL()+"/mocks", MIMEApplicationJSON, strings.NewReader(string(body)))
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())

			if res.StatusCode == http.StatusCreated {
				require.Equal(t, "/mocks/"+model.id, res.Header.Get(HeaderLocation))
			}

			return res.StatusCode
		}

		require.Equal(t, http.StatusCreated, post())

		defer m.storage.Delete(context.Background(), model.id)

		res, err := http.Get(m.AdminURL() + "/mocks/" + model.id)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)

		req := model.request.matching(t)
		res, got := send(t, m.URL(), req)

		served := model.served()[0]
		if model.random != nil {
			served = model.random[0]
			for _, r := range model.random {
				if replyDiff(r, req.method, res, got) == "" {
					served = r
				}
			}
		}

		require.Empty(t, replyDiff(served, req.method, res, got))
		require.Equal(t, http.StatusConflict, post(), "the id is taken")
	})
}

// Generators

// genDurationText draws strings near Go's duration syntax: one or more components, signs, odd units and forms
// such as 1. and .5. Values stay small, so none overflows a time.Duration.
func genDurationText() *rapid.Generator[string] {
	component := rapid.Custom(func(t *rapid.T) string {
		number := rapid.OneOf(
			rapid.StringMatching(`[0-9]{1,3}`),
			rapid.StringMatching(`[0-9]{1,3}\.[0-9]{0,2}`),
			rapid.StringMatching(`\.[0-9]{1,2}`),
			rapid.Just("."),
		).Draw(t, "number")

		unit := rapid.SampledFrom([]string{"ns", "us", "µs", "μs", "ms", "s", "m", "h", "", "x", "mss", " s", "S"}).Draw(t, "unit")

		return number + unit
	})

	return rapid.OneOf(
		rapid.Custom(func(t *rapid.T) string {
			sign := rapid.SampledFrom([]string{"", "", "", "+", "-"}).Draw(t, "sign")
			return sign + strings.Join(rapid.SliceOfN(component, 1, 3).Draw(t, "components"), "")
		}),
		rapid.SampledFrom([]string{"0", "00", "-0", "+0", "", ".s", "1.s", "0s"}),
	)
}

// genAny draws any value of a document.
func genAny(depth int) *rapid.Generator[any] {
	gens := []*rapid.Generator[any]{
		rapid.Just[any](nil),
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.Int64Range(-1000, 1000), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-10, 10), func(f float64) any { return f }),
		rapid.Map(rapid.SampledFrom([]string{"", "x", "gzip", "GET", "/x", "equal", "present", "[", "http://localhost", "1s", "-1"}), func(s string) any { return s }),
	}

	if depth > 0 {
		gens = append(gens,
			rapid.Map(rapid.SliceOfN(rapid.Deferred(func() *rapid.Generator[any] { return genAny(depth - 1) }), 0, 3), func(s []any) any { return s }),
			rapid.Map(rapid.MapOfN(rapid.SampledFrom([]string{"a", "status", "equal", "name"}), rapid.Deferred(func() *rapid.Generator[any] { return genAny(depth - 1) }), 0, 3),
				func(m map[string]any) any { return m }),
		)
	}

	return rapid.OneOf(gens...)
}

// genKey draws a key as users write them: mixed case, dots, spaces, YAML traps and any unicode.
func genKey() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.SampledFrom([]string{"userId", "UserID", "user_id", "a.b", "a b", "<<", "1", "true", "~", "Content-Type"}),
		rapid.String(),
	)
}

// Generators for the property tests of mock files. A model is a mock as Go values: it renders as a
// mock document, and it predicts how the served mock answers requests, which is what the tests check.

// _genMethods are the methods models use. HEAD is left out: its responses have no body to check.
var _genMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// _genStatuses are statuses whose responses carry a body and aren't redirects the client would follow.
var _genStatuses = []int{200, 201, 202, 203, 206, 400, 401, 403, 404, 409, 418, 422, 500, 503}

// genToken draws text that is safe as a header value, a query value or a path segment, and never holds '~',
// which samples add to make a value differ.
func genToken() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9_.-]{1,8}`)
}

// matcherModel is a matcher value of a mock file and what it matches.
type matcherModel struct {
	spec  any               // the value in the file: text, or a matcher list
	match func(string) bool // whether a request value matches; "" is a value that is absent
	// sample draws a request value that matches, or doesn't; "" means absent.
	sample func(t *rapid.T, matching bool) string
}

func genMatcher() *rapid.Generator[matcherModel] {
	return rapid.Custom(func(t *rapid.T) matcherModel {
		v := genToken().Draw(t, "value")

		// Matcher names are case-insensitive.
		name := func(n string) string {
			return rapid.SampledFrom([]string{n, strings.ToLower(n), strings.ToUpper(n)}).Draw(t, "name case")
		}

		equal := func(s string) bool { return s == v }
		equalSample := func(_ *rapid.T, matching bool) string {
			if matching {
				return v
			}

			return v + "~"
		}

		switch rapid.IntRange(0, 5).Draw(t, "matcher") {
		case 0: // Text is compared as is.
			return matcherModel{spec: v, match: equal, sample: equalSample}
		case 1:
			return matcherModel{spec: []any{name("equal"), v}, match: equal, sample: equalSample}
		case 2:
			return matcherModel{
				spec:  []any{name("eqi"), v},
				match: func(s string) bool { return strings.EqualFold(s, v) },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return flipCase(t, v)
					}

					return v + "~"
				},
			}
		case 3:
			return matcherModel{
				spec:  []any{name("hasPrefix"), v},
				match: func(s string) bool { return strings.HasPrefix(s, v) },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return v + genToken().Draw(t, "suffix")
					}

					return "~" + v
				},
			}
		case 4:
			return matcherModel{
				spec:  []any{name("contains"), v},
				match: func(s string) bool { return strings.Contains(s, v) },
				sample: func(_ *rapid.T, matching bool) string {
					if matching {
						return "~" + v + "~"
					}

					return "~"
				},
			}
		default:
			return matcherModel{
				spec:  []any{name("present")},
				match: func(s string) bool { return s != "" },
				sample: func(t *rapid.T, matching bool) string {
					if matching {
						return genToken().Draw(t, "present value")
					}

					return ""
				},
			}
		}
	})
}

// flipCase changes the case of some letters of s.
func flipCase(t *rapid.T, s string) string {
	runes := []rune(s)
	for i, r := range runes {
		if rapid.Bool().Draw(t, "flip") {
			if strings.ToUpper(string(r)) == string(r) {
				runes[i] = []rune(strings.ToLower(string(r)))[0]
			} else {
				runes[i] = []rune(strings.ToUpper(string(r)))[0]
			}
		}
	}

	return string(runes)
}

// requestModel is the request part of a mock model.
type requestModel struct {
	doc       map[string]any
	methods   []string // methods the mock accepts; all of them when hasMethod is false
	hasMethod bool
	path      string
	header    map[string]matcherModel
	query     map[string]matcherModel
	form      map[string]matcherModel
	multipart bool                      // the form is sent as multipart/form-data, with files
	files     map[string]fileFieldModel // form_file patterns, by field name
	body      *matcherModel
}

// fileFieldModel is a form_file pattern of a model: what the file of its field must have.
type fileFieldModel struct {
	filename    *matcherModel
	contentType string // "" when the pattern has no content_type
	body        *matcherModel
}

func genFileField() *rapid.Generator[fileFieldModel] {
	return rapid.Custom(func(t *rapid.T) fileFieldModel {
		var f fileFieldModel
		if rapid.Bool().Draw(t, "has filename") {
			f.filename = new(genMatcher().Draw(t, "filename"))
		}

		if rapid.Bool().Draw(t, "has content type") {
			f.contentType = rapid.SampledFrom([]string{"image/png", "text/plain"}).Draw(t, "content type")
		}

		if rapid.Bool().Draw(t, "has file body") {
			f.body = new(genMatcher().Draw(t, "file body"))
		}

		return f
	})
}

// spec is the pattern in a mock file.
func (f fileFieldModel) spec() map[string]any {
	spec := make(map[string]any)
	if f.filename != nil {
		spec["filename"] = f.filename.spec
	}

	if f.contentType != "" {
		spec["content_type"] = f.contentType
	}

	if f.body != nil {
		spec["body"] = f.body.spec
	}

	return spec
}

// matchingFile draws a file of field that the pattern matches.
func (f fileFieldModel) matchingFile(t *rapid.T, field string) formFile {
	file := formFile{
		field:       field,
		filename:    genToken().Draw(t, "any filename"),
		contentType: rapid.SampledFrom(_genFileTypes).Draw(t, "any content type"),
		body:        []byte(genToken().Draw(t, "any file body")),
	}

	if f.filename != nil {
		file.filename = f.filename.sample(t, true)
	}

	if f.contentType != "" {
		// The media type is compared in lower case and without parameters.
		file.contentType = rapid.SampledFrom([]string{f.contentType, strings.ToUpper(f.contentType), f.contentType + "; q=1"}).Draw(t, "content type form")
	}

	if f.body != nil {
		file.body = []byte(f.body.sample(t, true))
	}

	return file
}

func genRequest(baseURL string) *rapid.Generator[requestModel] {
	return rapid.Custom(func(t *rapid.T) requestModel {
		r := requestModel{doc: make(map[string]any)}
		r.path = "/" + strings.Join(rapid.SliceOfN(rapid.StringMatching(`[a-z0-9-]{1,6}`), 1, 3).Draw(t, "path"), "/")

		withBody := rapid.IntRange(0, 3).Draw(t, "body kind") // 0 none, 1 form, 2 text, 3 multipart form
		if withBody == 1 || withBody == 3 && rapid.Bool().Draw(t, "multipart has fields") {
			r.form = rapid.MapOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,6}`), genMatcher(), 1, 2).Draw(t, "form")
			r.doc["form"] = specs(r.form)
		}

		if withBody == 3 {
			r.multipart = true
			r.files = rapid.MapOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,6}`), genFileField(), 1, 2).Draw(t, "files")

			files := make(map[string]any, len(r.files))
			for field, f := range r.files {
				files[field] = f.spec()
			}

			r.doc["form_file"] = files
		}

		if withBody == 2 {
			body := genMatcher().Draw(t, "body")
			r.body = &body
			r.doc["body"] = body.spec
		}

		// A URL as text names the query too, so it goes without query matchers: an absolute one is compared with the
		// whole request URL, and a relative one with its path and query.
		wholeURL := false

		switch rapid.IntRange(0, 5).Draw(t, "url kind") {
		case 0:
			r.doc["path"] = r.path
		case 1:
			r.doc["path"] = []any{"equal", r.path}
		case 2:
			r.doc["url"] = r.path
			wholeURL = true
		case 3:
			r.doc["url"] = strings.ToUpper(baseURL[:4]) + baseURL[4:] + r.path
			wholeURL = true
		case 4:
			r.doc["url"] = []any{"urlPath", r.path}
		default:
			r.doc["url_match"] = `^http://[^/]+` + regexp.QuoteMeta(r.path) + `(\?.*)?$`
		}

		if !wholeURL && rapid.Bool().Draw(t, "has query") {
			// Mixed-case names: query names are case-sensitive, so their case must survive decoding.
			r.query = rapid.MapOfN(rapid.StringMatching(`[A-Za-z][A-Za-z0-9_]{0,6}`), genMatcher(), 1, 2).Draw(t, "query")
			r.doc["query"] = specs(r.query)
		}

		if rapid.Bool().Draw(t, "has header") {
			// Header names ignore case, so names that differ only in case would be one header.
			names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-[A-Za-z][A-Za-z0-9]{0,5}`), 1, 2, strings.ToLower).Draw(t, "header names")

			r.header = make(map[string]matcherModel, len(names))
			for _, name := range names {
				r.header[name] = genMatcher().Draw(t, "header")
			}

			r.doc["header"] = specs(r.header)
		}

		// A request with a body is a POST, so the methods hold POST.
		methods := rapid.SliceOfNDistinct(rapid.SampledFrom(_genMethods), 1, len(_genMethods), func(m string) string { return m }).Draw(t, "methods")
		if withBody > 0 && !slices.Contains(methods, http.MethodPost) {
			methods = append(methods, http.MethodPost)
		}

		r.methods = methods

		switch form := rapid.IntRange(0, 3).Draw(t, "method form"); {
		case form == 0 && withBody == 0:
			r.methods = _genMethods // no method key: any method
		case form == 1 && len(methods) == 1:
			r.hasMethod = true
			r.doc["method"] = strings.ToLower(methods[0]) // one method is compared ignoring case
		case form == 2:
			r.hasMethod = true
			r.doc["method"] = []any{"isIn", toAny(methods)}
		default:
			// Several methods are compared ignoring case too.
			r.hasMethod = true
			r.doc["method"] = strings.ToLower(strings.Join(methods, rapid.SampledFrom([]string{",", ", ", " ,"}).Draw(t, "separator")))
		}

		return r
	})
}

func specs(matchers map[string]matcherModel) map[string]any {
	out := make(map[string]any, len(matchers))
	for k, m := range matchers {
		out[k] = m.spec
	}

	return out
}

func toAny[E any](s []E) []any {
	out := make([]any, len(s))
	for i, e := range s {
		out[i] = e
	}

	return out
}

// requestSpec is a request to send.
type requestSpec struct {
	method    string
	path      string
	query     url.Values
	header    http.Header
	form      url.Values
	multipart bool       // send form and files as multipart/form-data
	files     []formFile // files of a multipart form
	body      *string
	what      string // what the request changes from a matching one, for failure messages
}

// matching draws a request the mock matches.
func (r requestModel) matching(t *rapid.T) requestSpec {
	req := requestSpec{
		method: rapid.SampledFrom(r.methods).Draw(t, "method"),
		path:   r.path,
		query:  make(url.Values),
		header: make(http.Header),
		what:   "matching",
	}

	if r.body != nil || r.form != nil || r.multipart {
		req.method = http.MethodPost
	}

	for k, m := range r.query {
		if v := m.sample(t, true); v != "" {
			req.query.Set(k, v)
		}
	}

	for k, m := range r.header {
		if v := m.sample(t, true); v != "" {
			req.header.Set(k, v)
		}
	}

	if r.form != nil {
		req.form = make(url.Values)
		for k, m := range r.form {
			if v := m.sample(t, true); v != "" {
				req.form.Set(k, v)
			}
		}
	}

	if r.multipart {
		req.multipart = true
		if req.form == nil {
			req.form = make(url.Values)
		}

		for _, field := range slices.Sorted(maps.Keys(r.files)) {
			req.files = append(req.files, r.files[field].matchingFile(t, field))
		}
	}

	if r.body != nil {
		body := r.body.sample(t, true)
		req.body = &body
	}

	return req
}

// mismatches draws requests that differ from a matching one in one matched part each.
func (r requestModel) mismatches(t *rapid.T) []requestSpec {
	var out []requestSpec

	if r.hasMethod && r.body == nil && r.form == nil && !r.multipart && len(r.methods) < len(_genMethods) {
		req := r.matching(t)
		req.method = rapid.SampledFrom(slices.DeleteFunc(slices.Clone(_genMethods), func(m string) bool {
			return slices.Contains(r.methods, m)
		})).Draw(t, "other method")
		req.what = "method " + req.method
		out = append(out, req)
	}

	req := r.matching(t)
	req.path += "x"
	req.what = "path " + req.path
	out = append(out, req)

	for k, m := range r.query {
		req := r.matching(t)
		setOrDelete(req.query, k, m.sample(t, false))
		req.what = "query " + k
		out = append(out, req)

		// Query names are case-sensitive: the same value under another case doesn't match.
		if other := flipCase(t, k); other != k {
			req := r.matching(t)
			v := req.query.Get(k)
			req.query.Del(k)
			if v == "" {
				v = genToken().Draw(t, "value")
			}

			req.query.Set(other, v)
			req.what = "query name " + other
			out = append(out, req)
		}
	}

	for k, m := range r.header {
		req := r.matching(t)
		if v := m.sample(t, false); v == "" {
			req.header.Del(k)
		} else {
			req.header.Set(k, v)
		}

		req.what = "header " + k
		out = append(out, req)
	}

	for k, m := range r.form {
		req := r.matching(t)
		setOrDelete(req.form, k, m.sample(t, false))
		req.what = "form " + k
		out = append(out, req)
	}

	if r.body != nil {
		req := r.matching(t)
		body := r.body.sample(t, false)
		req.body = &body
		req.what = "body"
		out = append(out, req)
	}

	out = append(out, r.fileMismatches(t)...)

	return out
}

// fileMismatches draws requests whose file of one field misses one condition of its pattern, or is gone.
func (r requestModel) fileMismatches(t *rapid.T) []requestSpec {
	var out []requestSpec

	for i, field := range slices.Sorted(maps.Keys(r.files)) {
		f := r.files[field]

		change := func(what string, edit func(file *formFile)) {
			req := r.matching(t)
			edit(&req.files[i])
			req.what = "file " + field + " " + what
			out = append(out, req)
		}

		if f.filename != nil {
			// A file without a name is a field to Go: the field then has no file, which doesn't match either.
			change("filename", func(file *formFile) { file.filename = f.filename.sample(t, false) })
		}

		if f.contentType != "" {
			change("content type", func(file *formFile) {
				file.contentType = map[string]string{"image/png": "text/plain", "text/plain": "image/png"}[f.contentType]
			})
		}

		if f.body != nil {
			change("body", func(file *formFile) { file.body = []byte(f.body.sample(t, false)) })
		}

		req := r.matching(t)
		req.files = slices.Delete(req.files, i, i+1)
		req.what = "file " + field + " gone"
		out = append(out, req)
	}

	return out
}

func setOrDelete(values url.Values, k, v string) {
	if v == "" {
		values.Del(k)
		return
	}

	values.Set(k, v)
}

// replyModel is a reply of a mock model and the response it predicts.
type replyModel struct {
	doc      map[string]any
	status   int
	header   []headerValue
	template []string // headers that render the request method
	trailer  []headerValue
	text     *string
	json     any // a body that is not text, served as JSON
	hasJSON  bool
	data     *string // a body rendering .Extra.name, set to this value
	gzip     bool

	multipart *multipartReplyModel // a multipart body
}

func genReply() *rapid.Generator[replyModel] {
	return rapid.Custom(func(t *rapid.T) replyModel {
		r := replyModel{doc: make(map[string]any)}

		r.status = rapid.SampledFrom(_genStatuses).Draw(t, "status")
		r.doc["status"] = int64(r.status)

		if names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-R[a-z0-9]{0,4}`), 0, 2, strings.ToLower).Draw(t, "headers"); len(names) > 0 {
			doc := make(map[string]any)
			for _, name := range names {
				values := rapid.SliceOfN(genToken(), 1, 2).Draw(t, "values")
				for _, v := range values {
					r.header = append(r.header, headerValue{name: name, value: v})
				}

				if len(values) == 1 && rapid.Bool().Draw(t, "one value as text") {
					doc[name] = values[0]
				} else {
					doc[name] = toAny(values)
				}
			}

			r.doc["header"] = doc
		}

		if names := rapid.SliceOfNDistinct(rapid.StringMatching(`X-T[a-z0-9]{0,4}`), 0, 2, strings.ToLower).Draw(t, "templates"); len(names) > 0 {
			doc := make(map[string]any)
			for _, name := range names {
				doc[name] = "{{ .Request.Method }}"
			}

			r.template = names
			r.doc["header_template"] = doc
		}

		if rapid.Bool().Draw(t, "has trailer") {
			v := genToken().Draw(t, "trailer")
			r.trailer = []headerValue{{name: "X-End", value: v}}
			r.doc["trailer"] = map[string]any{"X-End": v}
		}

		switch rapid.IntRange(0, 4).Draw(t, "body") {
		case 1:
			text := rapid.String().Draw(t, "text")
			r.text = &text
			r.doc["body"] = text
		case 2:
			r.json = genJSON(2).Draw(t, "json")
			r.hasJSON = true
			r.doc["body"] = r.json
		case 3:
			v := genToken().Draw(t, "data")
			r.data = &v
			r.doc["body"] = "{{ .Extra.name }}"
			r.doc["template"] = map[string]any{"enabled": true, "data": map[string]any{"name": v}}
		case 4:
			mp := genMultipartReply().Draw(t, "multipart")
			mp.gzip = false // the reply's own encoding gzips it

			// A mock file's string body is text, so the file has no byte bodies: they would render as templates.
			for i := range mp.parts {
				if mp.parts[i].kind == "bytes" {
					mp.parts[i].kind = "text"
				}
			}

			r.multipart = &mp
			r.doc["multipart"] = mp.doc()

			if mp.templated {
				r.doc["template"] = map[string]any{"enabled": true, "data": map[string]any{"v": mp.extra}}
			}
		}

		if rapid.Bool().Draw(t, "gzip") {
			r.gzip = true
			r.doc["encoding"] = "gzip"
		}

		return r
	})
}

// genJSON draws a body that isn't text: an object, a list, a number or a bool.
func genJSON(depth int) *rapid.Generator[any] {
	scalars := []*rapid.Generator[any]{
		rapid.Map(rapid.Bool(), func(b bool) any { return b }),
		rapid.Map(rapid.Int64Range(-1<<53, 1<<53), func(n int64) any { return n }),
		rapid.Map(rapid.Float64Range(-1e6, 1e6).Filter(func(f float64) bool { return f != float64(int64(f)) }), func(f float64) any { return f }),
	}

	// Inside a list or an object, values can also be text, null or nested lists.
	values := slices.Concat(scalars, []*rapid.Generator[any]{rapid.Map(genToken(), func(s string) any { return s }), rapid.Just[any](nil)})
	if depth > 0 {
		values = slices.Concat(values, []*rapid.Generator[any]{
			rapid.Map(rapid.SliceOfN(rapid.Deferred(func() *rapid.Generator[any] { return genJSON(depth - 1) }), 0, 3), func(s []any) any { return s }),
		})
	}

	return rapid.OneOf(slices.Concat(scalars, []*rapid.Generator[any]{
		rapid.Map(rapid.SliceOfN(rapid.OneOf(values...), 0, 3), func(s []any) any { return s }),
		rapid.Map(rapid.MapOfN(genToken(), rapid.OneOf(values...), 0, 3), func(m map[string]any) any { return m }),
	})...)
}

// mockModel is a mock as Go values.
type mockModel struct {
	id       string
	enabled  bool
	delay    time.Duration
	repeat   int64
	request  requestModel
	reply    *replyModel  // response
	sequence []replyModel // response_sequence
	after    *replyModel  // response_sequence.after_ended
	random   []replyModel // response_random
	seed     int64
}

func genMock(baseURL string) *rapid.Generator[mockModel] {
	return rapid.Custom(func(t *rapid.T) mockModel {
		m := mockModel{enabled: true, request: genRequest(baseURL).Draw(t, "request")}

		if rapid.Bool().Draw(t, "has id") {
			m.id = rapid.StringMatching(`[A-Za-z0-9._~-]{1,12}`).Draw(t, "id")
		}

		if rapid.IntRange(0, 9).Draw(t, "disabled") == 0 {
			m.enabled = false
		}

		if rapid.Bool().Draw(t, "has delay") {
			m.delay = time.Duration(rapid.IntRange(0, 3).Draw(t, "delay ms")) * time.Millisecond
		}

		if rapid.Bool().Draw(t, "has repeat") {
			m.repeat = rapid.Int64Range(1, 3).Draw(t, "repeat")
		}

		switch rapid.IntRange(0, 3).Draw(t, "reply kind") {
		case 0: // no response: 200 with nothing more
		case 1:
			r := genReply().Draw(t, "reply")
			m.reply = &r
		case 2:
			m.sequence = rapid.SliceOfN(genReply(), 1, 3).Draw(t, "sequence")
			if rapid.Bool().Draw(t, "has after_ended") {
				r := genReply().Draw(t, "after_ended")
				m.after = &r
			}
		default:
			m.random = rapid.SliceOfN(genReply(), 1, 3).Draw(t, "random")
			m.seed = rapid.Int64().Draw(t, "seed")
		}

		return m
	})
}

func (m mockModel) doc() map[string]any {
	doc := map[string]any{"request": m.request.doc}

	if m.id != "" {
		doc["id"] = m.id
	}

	if !m.enabled {
		doc["enabled"] = false
	}

	if m.delay > 0 {
		doc["delay"] = m.delay.String()
	}

	if m.repeat > 0 {
		doc["repeat"] = m.repeat
	}

	replies := func(rs []replyModel) []any {
		out := make([]any, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.doc)
		}

		return out
	}

	switch {
	case m.reply != nil:
		doc["response"] = m.reply.doc
	case m.sequence != nil:
		seq := map[string]any{"responses": replies(m.sequence)}
		if m.after != nil {
			seq["after_ended"] = m.after.doc
		}

		doc["response_sequence"] = seq
	case m.random != nil:
		doc["response_random"] = map[string]any{"responses": replies(m.random), "seed": m.seed}
	}

	return doc
}

// served lists the replies of the requests the mock answers, in order, up to what the test sends.
func (m mockModel) served() []replyModel {
	var out []replyModel

	switch {
	case m.reply != nil:
		out = []replyModel{*m.reply, *m.reply}
	case m.sequence != nil:
		out = slices.Clone(m.sequence)
		if m.after != nil {
			out = append(out, *m.after, *m.after)
		}
	case m.random != nil:
		out = []replyModel{{}, {}} // checked against every reply of random
	default:
		out = []replyModel{{status: http.StatusOK}, {status: http.StatusOK}}
	}

	if m.repeat > 0 && int64(len(out)) > m.repeat {
		out = out[:m.repeat]
	}

	return out
}

// encodeMock writes a mock document in a format.
func encodeMock(t *rapid.T, doc map[string]any, format string) []byte {
	b, err := config.Encode(doc, format)
	if err != nil {
		t.Fatalf("encoding the mock as %s: %v", format, err)
	}

	return b
}
