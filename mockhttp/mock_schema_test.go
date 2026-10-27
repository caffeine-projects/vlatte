package mockhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/config"
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
		{"event_stream as text", ok + "response: {event_stream: x}\n", []want{{config.CodeInvalidValue, "response.event_stream", "must be an object, not a string", ""}}},
		{"event_stream and body", ok + "response: {body: x, event_stream: {}}\n", []want{{config.CodeConflict, "response.event_stream", "response.event_stream is set with response.body", "keep one of them"}}},
		{"event_stream and multipart", ok + "response: {multipart: {type: mixed, parts: [{}]}, event_stream: {}}\n", []want{{config.CodeConflict, "response.event_stream", "response.event_stream is set with response.multipart", "keep one of them"}}},
		{"event raw with data", ok + "response: {event_stream: {events: [{raw: x, data: y}]}}\n", []want{{config.CodeConflict, "response.event_stream.events[0].raw", "response.event_stream.events[0].raw is set with other fields", "raw is sent alone, as it is, with a delay at most"}}},
		{"event data and data_json", ok + "response: {event_stream: {events: [{data: x, data_json: y}]}}\n", []want{{config.CodeConflict, "response.event_stream.events[0].data_json", "response.event_stream.events[0].data_json is set with response.event_stream.events[0].data", "keep one of them"}}},
		{"event data_json JSON can't write", ok + "response: {event_stream: {events: [{data_json: .nan}]}}\n", []want{{config.CodeInvalidValue, "response.event_stream.events[0].data_json", `response.event_stream.events[0].data_json="NaN": json: `, ""}}},
		{"event null data", ok + "response: {event_stream: {events: [{}, {data: null}]}}\n", []want{{config.CodeInvalidValue, "response.event_stream.events[1].data", "is null", `write "" for empty data`}}},
		{"event data JSON can't write", ok + "response: {event_stream: {events: [{data: .nan}]}}\n", []want{{config.CodeInvalidValue, "response.event_stream.events[0].data", `response.event_stream.events[0].data="NaN": json: `, ""}}},
		{"event negative delay", ok + "response: {event_stream: {events: [{delay: -1}]}}\n", []want{{config.CodeInvalidValue, "response.event_stream.events[0].delay", "must be 0 or more", ""}}},
		{"event key with a typo", ok + "response: {event_stream: {events: [{dat: x}]}}\n", []want{{config.CodeUnknownKey, "response.event_stream.events[0].dat", "not a known setting", "did you mean response.event_stream.events[0].data?"}}},
		{"events as an object", ok + "response: {event_stream: {events: {data: x}}}\n", []want{{config.CodeInvalidValue, "response.event_stream.events", "must be a list", ""}}},
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
	// WireMock's customMatcher: a matcher of the server's registry, by name with its arguments, gets the request.
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
