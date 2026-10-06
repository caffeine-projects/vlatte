package e2e

import (
	"bytes"
	"crypto/tls"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
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

	sHTTP := m.MustMock(mockhttp.FromFile("testdata/matchers/01_matchers.yaml"))
	sHTTPS := m.MustMock(mockhttp.FromFile("testdata/matchers/02_matchers.yaml"))

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
	u := m.URL()
	actAndAssert(u)
	m.Close()

	m.MustStartTLS()
	u = m.URL()
	actAndAssert(u)
	m.Close()

	require.True(t, vlatte.Times(t, sHTTP, 1))
	require.True(t, vlatte.Times(t, sHTTPS, 1))

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
	m.MustMock(mockhttp.FromFile("testdata/matchers/03_url_template.yaml"))

	client := &http.Client{}
	res, err := client.Get(m.URL("/test?q=none"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	res, err = client.Post(m.URL("/test?q=none"), mockhttp.MIMETextPlain, nil)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	req, _ := http.NewRequest(http.MethodPut, m.URL("/test?q=none"), nil)
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
	m := mockhttp.MustNew()
	m.MustStart()
	m.MustMock(mockhttp.Getf("/test").URLFragmentf("contacts").Reply(mockhttp.OK().BodyText("hello world")))

	t.Cleanup(func() { m.Close() })

	// no fragment
	client := &http.Client{}
	res, err := client.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

	// wrong fragment
	client = &http.Client{}
	res, err = client.Get(m.URL("/test#wrong-value"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

	// the right fragment value
	res, err = client.Get(m.URL("/test#contacts"))
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello world", string(b))
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
