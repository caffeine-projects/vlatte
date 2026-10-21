package mockhttp

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/match"
)

// statusOf sends req and returns the status of the response.
func statusOf(t *testing.T, req *http.Request) int {
	t.Helper()

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res.StatusCode
}

func TestBuilder_HeadersReadsEveryValue(t *testing.T) {
	// Header reads the first value of a header only: a request sending X-Tag twice could match only on its first, and
	// one sending it empty looked like one without it. Headers reads every value, in order, and nil when absent.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/second").Headers("X-Tag", match.Some(match.Equal("b"))).Reply(OK()))
	m.MustMock(Getf("/ordered").Headers("X-Tag", match.Equal([]string{"a", "b"})).Reply(OK()))
	m.MustMock(Getf("/absent").Headers("X-Tag", match.Not(match.Present())).Reply(OK()))

	tests := []struct {
		name   string
		path   string
		values []string // the X-Tag lines of the request; nil sends none
		want   int
	}{
		{"a value after the first", "/second", []string{"a", "b"}, http.StatusOK},
		{"only the first value", "/second", []string{"a"}, StatusNoMatch},
		{"values in order", "/ordered", []string{"a", "b"}, http.StatusOK},
		{"values in another order", "/ordered", []string{"b", "a"}, StatusNoMatch},
		{"one line is one value", "/ordered", []string{"a, b"}, StatusNoMatch},
		{"no header", "/absent", nil, http.StatusOK},
		{"a header sent empty", "/absent", []string{""}, StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.values != nil {
				req.Header["X-Tag"] = tt.values
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_FormFieldsReadsEveryValueOfTheBody(t *testing.T) {
	// FormField reads the first value of a field only. FormFields reads every value, and, like FormField, never the
	// query: a ?tag= must not complete a form.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Postf("/tags").FormFields("tag", match.Equal([]string{"a", "b"})).Reply(OK()))

	tests := []struct {
		name  string
		query string
		body  string
		want  int
	}{
		{"every value", "", "tag=a&tag=b", http.StatusOK},
		{"a value missing", "", "tag=a", StatusNoMatch},
		{"a value too many", "", "tag=a&tag=b&tag=c", StatusNoMatch},
		{"a value in the query", "?tag=b", "tag=a", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := post(t, m.URL("/tags"+tt.query), MIMEFormURLEncoded, []byte(tt.body))
			require.Equal(t, tt.want, res.StatusCode)
		})
	}
}

func TestBuilder_Cookie(t *testing.T) {
	// A cookie matches by its name, which keeps its case, as browsers keep it: Session isn't session. A request without
	// the cookie isn't one that sends it empty.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/me").Cookie("session", match.Equal("abc")).Reply(OK()))
	m.MustMock(Getf("/empty").Cookie("session", match.Equal("")).Reply(OK()))

	tests := []struct {
		name   string
		path   string
		cookie string // the Cookie header; "" sends none
		want   int
	}{
		{"the cookie", "/me", "session=abc", http.StatusOK},
		{"among others", "/me", "theme=dark; session=abc", http.StatusOK},
		{"another value", "/me", "session=xyz", StatusNoMatch},
		{"another case of its name", "/me", "Session=abc", StatusNoMatch},
		{"no cookie", "/me", "", StatusNoMatch},
		{"an empty cookie", "/empty", "session=", http.StatusOK},
		{"no cookie for an empty one", "/empty", "", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.cookie != "" {
				req.Header.Set(HeaderCookie, tt.cookie)
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_BasicAuth(t *testing.T) {
	// Basic credentials match as the request sends them, and a request without them matches no credentials, empty
	// ones included: a mock for an empty user must not answer anonymous requests.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/admin").BasicAuth("jeff", "s3cret").Reply(OK()))
	m.MustMock(Getf("/empty").BasicAuth("", "").Reply(OK()))
	m.MustMock(Getf("/anonymous").BasicAuthMatches(match.Not(match.Present()), match.Anything()).Reply(OK()))

	tests := []struct {
		name string
		path string
		auth func(r *http.Request)
		want int
	}{
		{"the credentials", "/admin", func(r *http.Request) { r.SetBasicAuth("jeff", "s3cret") }, http.StatusOK},
		{"another password", "/admin", func(r *http.Request) { r.SetBasicAuth("jeff", "S3cret") }, StatusNoMatch},
		{"a bearer token", "/admin", func(r *http.Request) { r.Header.Set(HeaderAuthorization, "Bearer jeff:s3cret") }, StatusNoMatch},
		{"no credentials", "/admin", func(*http.Request) {}, StatusNoMatch},
		{"empty credentials", "/empty", func(r *http.Request) { r.SetBasicAuth("", "") }, http.StatusOK},
		{"no credentials for empty ones", "/empty", func(*http.Request) {}, StatusNoMatch},
		{"no credentials when none are wanted", "/anonymous", func(*http.Request) {}, http.StatusOK},
		{"credentials when none are wanted", "/anonymous", func(r *http.Request) { r.SetBasicAuth("jeff", "s3cret") }, StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)
			tt.auth(req)

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestBuilder_Host(t *testing.T) {
	// The host is the one the request names, without its port, which the client picks: a mock for api.local answers
	// api.local:8080 and API.local, as host names ignore case.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/x").Host(match.EqualIgnoreCase("api.local")).Reply(OK()))
	m.MustMock(Getf("/v6").Host(match.Equal("::1")).Reply(OK()))

	tests := []struct {
		name string
		path string
		host string
		want int
	}{
		{"the host", "/x", "api.local", http.StatusOK},
		{"with a port", "/x", "api.local:8080", http.StatusOK},
		{"in another case", "/x", "API.local", http.StatusOK},
		{"another host", "/x", "api.local.evil", StatusNoMatch},
		{"the server's own", "/x", "", StatusNoMatch},
		{"an IPv6 address, without its brackets", "/v6", "[::1]:8080", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, m.URL(tt.path), nil)
			require.NoError(t, err)

			if tt.host != "" {
				req.Host = tt.host
			}

			require.Equal(t, tt.want, statusOf(t, req))
		})
	}
}

func TestForwardProxy_HostMatchesTheHostTheClientAskedFor(t *testing.T) {
	// A forward proxy gets requests in absolute form: Host matches the host they name, so a mock can answer one host
	// and leave the others to the proxy.
	m := MustNew(Setup().Silent().Proxy())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Getf("/users").Host(match.EqualIgnoreCase("api.example.com")).Reply(OK().PlainText("mocked")))

	client := proxyClient(t, m)
	body := func(target string) string {
		res, err := client.Get(target)
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return string(b)
	}

	require.Equal(t, "mocked", body("http://api.example.com/users"))
	// Nothing listens on port 1: the proxy fails to send it on, fast.
	require.NotEqual(t, "mocked", body("http://127.0.0.1:1/users"), "another host goes on to that host")
}

func TestBuilder_ClientIP(t *testing.T) {
	// The client's address is the connection's: an X-Forwarded-For header, which any client can send, doesn't change
	// it.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	// httptest listens on 127.0.0.1, or on ::1 where there is no IPv4.
	m.MustMock(Getf("/local").ClientIP(match.IsIn([]string{"127.0.0.1", "::1"})).Reply(OK()))
	m.MustMock(Getf("/office").ClientIP(match.Equal("10.0.0.1")).Reply(OK()))

	send := func(path, forwardedFor string) int {
		req, err := http.NewRequest(http.MethodGet, m.URL(path), nil)
		require.NoError(t, err)

		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}

		return statusOf(t, req)
	}

	require.Equal(t, http.StatusOK, send("/local", ""))
	require.Equal(t, StatusNoMatch, send("/office", ""))
	require.Equal(t, StatusNoMatch, send("/office", "10.0.0.1"), "X-Forwarded-For isn't the client's address")
}

func TestMockFile_HostTakesAnIPv6Address(t *testing.T) {
	// host refuses text with a port or brackets, which would never match: an IPv6 address alone has colons, and is
	// neither.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(FromBytes([]byte("request: {path: /v6, host: '::1'}\n"), "yaml"))

	req, err := http.NewRequest(http.MethodGet, m.URL("/v6"), nil)
	require.NoError(t, err)
	req.Host = "[::1]:8080"

	require.Equal(t, http.StatusOK, statusOf(t, req))
}

func TestBuilder_URLPathTemplate(t *testing.T) {
	// A path template matches paths of its shape, and PathParam the value of a parameter, whichever was set first.
	m := MustNew(Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(Request().Method(http.MethodGet).
		PathParam("id", match.Equal("7")).
		URLPathTemplate("/users/{id}/orders/{orderId}").
		PathParam("orderId", match.HasPrefix("BR-")).
		Reply(OK()))
	m.MustMock(Request().Method(http.MethodGet).URLPathTemplate("/files/{name}").Reply(OK()))

	tests := []struct {
		path string
		want int
	}{
		{"/files/a.txt", http.StatusOK},
		{"/files", StatusNoMatch},
		{"/files/", StatusNoMatch},
		{"/users/7/orders/BR-1", http.StatusOK},
		{"/users/8/orders/BR-1", StatusNoMatch},
		{"/users/7/orders/US-1", StatusNoMatch},
		{"/users/7/orders", StatusNoMatch},
		{"/users/7/orders/BR-1/", StatusNoMatch},
		{"/users/7/orders/BR-1/items", StatusNoMatch},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			status, _ := get(t, m, tt.path)
			require.Equal(t, tt.want, status)
		})
	}
}

func TestBuilder_PathTemplateErrors(t *testing.T) {
	// A path parameter needs a template that has it, and a mock has one template: anything else fails the mock,
	// rather than a matcher that never matches.
	m := MustNew(Setup().Silent())

	tests := []struct {
		name    string
		give    *Builder
		wantErr string
	}{
		{
			"no template", Request().Method(http.MethodGet).PathParam("id", match.Anything()),
			`mock: path param "id": the mock has no path template: set one with URLPathTemplate`,
		},
		{
			"a parameter the template doesn't have", Request().URLPathTemplate("/users/{id}").PathParam("userId", match.Anything()),
			`mock: path param "userId": path template "/users/{id}" has no such parameter`,
		},
		{
			"a second template", Request().URLPathTemplate("/a/{id}").URLPathTemplate("/b/{id}"),
			`mock: path template "/b/{id}": the mock has path template "/a/{id}" already`,
		},
		{
			"a template that doesn't parse", Request().URLPathTemplate("/users/a{id}"),
			`mock: path template "/users/a{id}": segment "a{id}": a parameter is a whole segment, such as {id}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.give.Reply(OK()).Build(m)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}
