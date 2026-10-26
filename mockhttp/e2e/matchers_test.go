package e2e

import (
	"bytes"
	"crypto/tls"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/match/matchbuild"
	"github.com/caffeine-projects/vlatte/mockhttp"
)

func TestMatcherCombinations(t *testing.T) {
	type addr struct {
		Str     string  `json:"str,omitempty"`
		Number  int     `json:"number,omitzero"`
		Country *string `json:"country"`
	}

	type model struct {
		Name       string   `json:"name,omitempty"`
		Hobbies    []string `json:"hobbies,omitempty"`
		Job        string   `json:"job,omitempty"`
		Active     bool     `json:"active,omitzero"`
		Plans      []int    `json:"plans,omitempty"`
		Acronym    string   `json:"acronym,omitempty"`
		Title      string   `json:"title,omitempty"`
		Addr       addr     `json:"addr"`
		CheckIn    bool     `json:"check_in"` // sent when false: falsy doesn't match a missing field
		Activities string   `json:"activities,omitempty"`
		Department string   `json:"department,omitempty"`
		Place      string   `json:"place,omitempty"`
		Numbers    []int    `json:"numbers,omitempty"`

		Days int `json:"days,omitzero"`
	}

	type response struct {
		Ok   bool    `json:"ok,omitzero"`
		Type string  `json:"type,omitempty"`
		Num  float64 `json:"num,omitzero"`
	}

	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	m := mockhttp.MustNew()
	ms := mockhttp.MustNew()

	// Both servers hold both mocks, which differ only in the scheme they take: each request matches its scheme's.
	sHTTP := m.MustMock(mockhttp.FromFile("testdata/matchers/01_matchers.yaml"))
	httpsOnHTTP := m.MustMock(mockhttp.FromFile("testdata/matchers/02_matchers.yaml"))
	httpOnHTTPS := ms.MustMock(mockhttp.FromFile("testdata/matchers/01_matchers.yaml"))
	sHTTPS := ms.MustMock(mockhttp.FromFile("testdata/matchers/02_matchers.yaml"))

	actAndAssert := func(baseURL string) {
		body := &model{
			Name:    "hello world",
			Hobbies: []string{"bike", "trekking"},
			Job:     "dev",
			Active:  true,
			Acronym: "QA",
			Title:   "Software Engineer",
			Addr: addr{
				Str:     "Nowhere",
				Number:  100,
				Country: nil,
			},
			CheckIn:    false,
			Activities: "walk+run+swim",
			Department: "  Bombs  ",
			Place:      "Berlin",
			Numbers:    []int{10, 20, 30, 40, 50},
			Days:       10,
		}

		buf := new(bytes.Buffer)
		err := json.MarshalWrite(buf, body)
		require.NoError(t, err)

		qry := url.Values{}
		qry.Add("q", "test")
		qry.Add("sort", "asc")
		qry.Add("page", "5")
		qry.Add("lang", "en")
		qry.Add("test", "true")
		qry.Add("tags", "link,detail,price")
		qry.Add("ctx", "hello world")
		qry.Add("nm", "hello")
		qry.Add("nm", "hi")
		qry.Add("nm", "hallo")

		u := baseURL + "/test?" + qry.Encode() + "&age(150)&contains(nothing)"
		req, _ := http.NewRequest(http.MethodPost, u, buf)
		req.Header.Add(mockhttp.HeaderContentType, mockhttp.MIMEApplicationJSON)
		req.Header.Add(mockhttp.HeaderAccept, mockhttp.MIMEApplicationJSON)
		req.Header.Add("x-test", "dev, qa, devops")

		res, err := httpClient.Do(req)
		require.NoError(t, err)

		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		r := new(response)
		err = json.Unmarshal(b, r)

		require.NoError(t, err, u)
		require.Equal(t, mockhttp.MIMEApplicationJSON, res.Header.Get(mockhttp.HeaderContentType))
		require.Equal(t, "success", res.Header.Get("x-custom"))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, true, r.Ok)
		require.Equal(t, "test", r.Type)
		require.EqualValues(t, 10, r.Num)
	}

	m.MustStart()
	actAndAssert(m.URL())
	m.Close()

	ms.MustStartTLS()
	actAndAssert(ms.URL())
	ms.Close()

	require.True(t, vlatte.Times(t, sHTTP, 1))
	require.True(t, vlatte.Times(t, sHTTPS, 1))
	require.True(t, vlatte.NotCalled(t, httpsOnHTTP))
	require.True(t, vlatte.NotCalled(t, httpOnHTTPS))

	m = mockhttp.MustNew()
	m.MustStart()
	defer m.Close()

	entry := sHTTP.Mocks()[0]
	b, err := entry.MarshalJSON()
	require.NoError(t, err)

	fmt.Println(string(b))

	m.MustMock(mockhttp.FromBytes(b, "json"))

	actAndAssert(m.URL())
}

func TestMatchers_MultipleMethods(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/matchers/03_multiple_methods.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL() + "/test?q=none")

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, err = client.Post(m.URL()+"/test?q=none", mockhttp.MIMETextPlain, nil)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	req, _ := http.NewRequest(http.MethodPut, m.URL()+"/test?q=none", nil)
	res, err = client.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestMatchers_URLMatchRegex(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/matchers/04_url_match.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL("/test?q=hi"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, err = client.Get(m.URL("/test?q=bye"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestMatchers_URLCustomMatcher(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/matchers/05_url_custom_matcher.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL("/test?q=hi"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, err = client.Get(m.URL("/test?q=bye"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestMatchers_PathCustomMatcher(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/matchers/06_path_custom_matcher.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL("/hi"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, err = client.Get(m.URL("/bye"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}

func TestMatchers_URLFragment(t *testing.T) {
	// The fragment is what follows a "#" of the request target, after the query. An escaped "%23" is part of the path.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	// The mock matches any path, so each request misses on its fragment only.
	m.MustMock(mockhttp.Request().URLFragmentf("contacts").Reply(mockhttp.OK()))

	tests := []struct {
		give       string
		wantStatus int
	}{
		{give: "/test", wantStatus: mockhttp.StatusNoMatch},
		{give: "/test#wrong-value", wantStatus: mockhttp.StatusNoMatch},
		{give: "/test%23contacts", wantStatus: mockhttp.StatusNoMatch},
		{give: "/test#contacts", wantStatus: http.StatusOK},
		{give: "/test?q=1#contacts", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.give, func(t *testing.T) {
			res, body := getTarget(t, m, tt.give)
			require.Equal(t, tt.wantStatus, res.StatusCode, string(body))
		})
	}
}

func TestMatchers_URLFragmentFromAMockFile(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/matchers/08_url_fragment.yaml"))

	res, body := getTarget(t, m, "/test#contacts")
	require.Equal(t, http.StatusNoContent, res.StatusCode, string(body))

	res, body = getTarget(t, m, "/test#other")
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, string(body))
}

func TestMatchers_RequestParts(t *testing.T) {
	// Each key of the file matches its part of the request: the whole request matches, and one that misses any part
	// doesn't. The client's address is always this machine's, so client_ip only has to match.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/matchers/09_request_parts.yaml"))

	send := func(t *testing.T, form string, spoil func(r *http.Request)) int {
		req, err := http.NewRequest(http.MethodPost, m.URL("/login"), strings.NewReader(form))
		require.NoError(t, err)

		req.Host = "api.local:8080"
		req.Header.Set(mockhttp.HeaderContentType, mockhttp.MIMEFormURLEncoded)
		req.Header["Accept"] = []string{"application/json", "text/html"}
		req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
		req.SetBasicAuth("jeff", "s3cret")
		spoil(req)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		return res.StatusCode
	}

	require.Equal(t, http.StatusNoContent, send(t, "tag=a&tag=b", func(*http.Request) {}))

	tests := []struct {
		name  string
		form  string
		spoil func(r *http.Request)
	}{
		{"host", "tag=a&tag=b", func(r *http.Request) { r.Host = "other.local" }},
		{"headers", "tag=a&tag=b", func(r *http.Request) { r.Header["Accept"] = []string{"text/html"} }},
		{"cookie", "tag=a&tag=b", func(r *http.Request) { r.Header.Del(mockhttp.HeaderCookie) }},
		{"basic_auth", "tag=a&tag=b", func(r *http.Request) { r.Header.Del(mockhttp.HeaderAuthorization) }},
		{"forms", "tag=a", func(*http.Request) {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, mockhttp.StatusNoMatch, send(t, tt.form, tt.spoil))
		})
	}
}

func TestMatchers_PathTemplate(t *testing.T) {
	// path_template matches paths of its shape, path_params their parameters, and the reply reads them by name.
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.FromFile("testdata/matchers/10_path_template.yaml"))

	res, body := getTarget(t, m, "/users/42/orders/BR-7")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	require.Equal(t, "BR-7 of 42", string(body))

	// A failure answers 418 too: the header says no mock matched.
	for _, target := range []string{"/users/me/orders/BR-7", "/users/42/orders", "/users/42/orders/BR-7/"} {
		res, body = getTarget(t, m, target)
		require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode, "%s: %s", target, body)
		require.Equal(t, vlatte.FailureNoMatch, res.Header.Get(mockhttp.HeaderVlatteError), "%s: %s", target, body)
	}
}

func TestMatchers_QueryLeavesTheFragmentOut(t *testing.T) {
	// Go's server leaves a "#" in the query it reads: q would be "1#contacts".
	m := mockhttp.MustNew(mockhttp.Setup().Silent())
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/test").Queryf("q", "1").Reply(mockhttp.OK()))

	res, body := getTarget(t, m, "/test?q=1#contacts")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
}

func TestMatchers_AMiddlewareSeesThePathWithoutTheFragment(t *testing.T) {
	// The fragment leaves the path before any middleware runs, so a middleware that rewrites the path keeps both.
	m := mockhttp.MustNew(mockhttp.Setup().Silent().Use(func(h http.Handler) http.Handler {
		return http.StripPrefix("/api", h)
	}))
	m.MustStart()
	t.Cleanup(m.Close)

	m.MustMock(mockhttp.Getf("/users").URLFragmentf("top").Reply(mockhttp.OK()))

	res, body := getTarget(t, m, "/api/users#top")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
}

// getTarget sends a GET with target as the request target, as is, and reads the whole response: Go's http.Client
// doesn't send a URL's fragment.
func getTarget(t *testing.T, m *mockhttp.Server, target string) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, m.URL(), nil)
	require.NoError(t, err)
	req.URL.Opaque = target

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	return res, b
}

func TestMatchers_NoReply_ShouldReturn200ByDefault(t *testing.T) {
	m := mockhttp.MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.FromFile("testdata/matchers/07_no_reply.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMatchers_WireMockParity(t *testing.T) {
	// The matchers WireMock has and vlatte lacked, each from a mock file, on a server with a custom matcher. A second
	// server holds each mock as the admin API's store keeps it, EncodeMock's description: every request must be
	// answered the same by both, so no matcher is lost on the way.
	hasHeader := func(_ *matchbuild.Registry, args []any) (vlatte.Matcher, error) {
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

	setup := func() *mockhttp.ConfigBuilder {
		return mockhttp.Setup().Silent().Matchers(matchbuild.MustNew(matchbuild.WithMatcher("has-header", hasHeader)))
	}

	files := mockhttp.MustNew(setup())
	files.MustStart()
	t.Cleanup(files.Close)

	stored := mockhttp.MustNew(setup())
	stored.MustStart()
	t.Cleanup(stored.Close)

	ids := []string{"json", "schema", "xml", "xpath", "dates", "exactly", "custom"}
	scope := files.MustMock(
		mockhttp.FromFile("testdata/matchers/11_equaljson_options.yaml"),
		mockhttp.FromFile("testdata/matchers/12_jsonschema.yaml"),
		mockhttp.FromFile("testdata/matchers/13_equalxml.yaml"),
		mockhttp.FromFile("testdata/matchers/14_xpath.yaml"),
		mockhttp.FromFile("testdata/matchers/15_dates.yaml"),
		mockhttp.FromFile("testdata/matchers/16_hasexactly.yaml"),
		mockhttp.FromFile("testdata/matchers/17_custom.yaml"),
	)

	for _, id := range ids {
		doc, err := mockhttp.EncodeMock(scope.Mock(id), "yaml")
		require.NoError(t, err, id)
		stored.MustMock(mockhttp.FromBytes(doc, "yaml"))
	}

	now := time.Now().UTC()
	todo := `<todo-list xmlns="urn:todo"><todo-item>wash the car</todo-item><todo-item>walk the dog</todo-item></todo-list>`

	tests := []struct {
		name   string
		method string
		target string
		header http.Header
		body   string
		want   int
	}{
		{"json: any order, extra elements", http.MethodPost, "/json", nil, `{"tags": ["c", "b", "a"], "name": "Rex", "age": 3}`, http.StatusOK},
		{"json: a missing item", http.MethodPost, "/json", nil, `{"tags": ["b"], "name": "Rex"}`, mockhttp.StatusNoMatch},
		{"schema: conforms", http.MethodPost, "/schema", nil, `{"name": "Rex", "age": 3}`, http.StatusOK},
		{"schema: doesn't", http.MethodPost, "/schema", nil, `{"name": "Rex", "age": -3}`, mockhttp.StatusNoMatch},
		{"xml: children in any order", http.MethodPost, "/xml", nil, "<order id=\"7\">\n  <item>2</item>\n  <item>1</item>\n</order>", http.StatusOK},
		{"xml: another attribute", http.MethodPost, "/xml", nil, `<order id="8"><item>1</item><item>2</item></order>`, mockhttp.StatusNoMatch},
		{"xpath: in the namespace", http.MethodPost, "/xpath", nil, todo, http.StatusOK},
		{"xpath: another namespace", http.MethodPost, "/xpath", nil, strings.ReplaceAll(todo, "urn:todo", "urn:other"), mockhttp.StatusNoMatch},
		{
			"dates: within the range", http.MethodGet, "/dates?until=" + url.QueryEscape(now.Add(72*time.Hour).Format(time.RFC3339)),
			http.Header{"X-Date": {now.Format(http.TimeFormat)}},
			"", http.StatusOK,
		},
		{
			"dates: too old", http.MethodGet, "/dates?until=" + url.QueryEscape(now.Add(72*time.Hour).Format(time.RFC3339)),
			http.Header{"X-Date": {now.Add(-2 * time.Hour).Format(http.TimeFormat)}},
			"", mockhttp.StatusNoMatch,
		},
		{
			"dates: too late", http.MethodGet, "/dates?until=" + url.QueryEscape(now.Add(240*time.Hour).Format(time.RFC3339)),
			http.Header{"X-Date": {now.Format(http.TimeFormat)}},
			"", mockhttp.StatusNoMatch,
		},
		{"exactly: each value its matcher", http.MethodGet, "/exactly?id=20&id=1", nil, "", http.StatusOK},
		{"exactly: one too many", http.MethodGet, "/exactly?id=20&id=1&id=3", nil, "", mockhttp.StatusNoMatch},
		{"custom: the header", http.MethodGet, "/custom", http.Header{"X-Admin": {"1"}}, "", http.StatusOK},
		{"custom: no header", http.MethodGet, "/custom", nil, "", mockhttp.StatusNoMatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, server := range []*mockhttp.Server{files, stored} {
				req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL(tc.target), strings.NewReader(tc.body))
				require.NoError(t, err)

				maps.Copy(req.Header, tc.header)

				res, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, tc.want, res.StatusCode)
			}
		})
	}
}
