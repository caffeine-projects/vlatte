package test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte/bean/xhttp"
	"github.com/caffeine-projects/vlatte/bean/xhttp/httpval"
	"github.com/caffeine-projects/vlatte/matcher"
	"github.com/stretchr/testify/require"
)

func TestFormUrlEncoded(t *testing.T) {
	httpClient := &http.Client{}
	m := xhttp.NewAPI().CloseWithT(t)
	m.MustStart()

	scoped := m.MustMock(xhttp.Post(matcher.URLPath("/test")).
		FormField("var1", matcher.StrictEqual("dev")).
		FormField("var2", matcher.Contain("q")).
		Reply(xhttp.OK()))

	data := url.Values{}
	data.Set("var1", "dev")
	data.Set("var2", "qa")

	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", strings.NewReader(data.Encode()))
	req.Header.Add("test", "hello")
	req.Header.Add(httpval.HeaderContentType, httpval.MIMEFormURLEncoded)
	res, err := httpClient.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.True(t, scoped.Called())
}

func TestFormUrlEncoded_InvalidFieldValues(t *testing.T) {
	m := xhttp.NewAPI().CloseWithT(t)
	scoped, err := m.Mock(xhttp.FromFile("testdata/form_url_encoded/01_invalid.yaml"))

	require.Nil(t, scoped)
	require.Error(t, err)
}

func TestFormUrlEncoded_FromFileMock(t *testing.T) {
	httpClient := &http.Client{}
	m := xhttp.NewAPI().CloseWithT(t)
	m.MustStart()

	scoped := m.MustMock(xhttp.FromFile("testdata/form_url_encoded/02_valid.yaml"))

	testCases := []struct {
		name           string
		data           func() url.Values
		expectedStatus int
	}{
		{"match", func() url.Values {
			data := url.Values{}
			data.Set("name", "nice name")
			data.Set("address", "berlin+germany")
			data.Set("active", "true")
			data.Set("live", "false")
			data.Set("money", "2550.50")
			data.Set("code", "10")
			data.Set("job", "dev")

			return data
		}, http.StatusNoContent},

		{"no match (not equals)", func() url.Values {
			data := url.Values{}
			data.Set("name", "nice name")
			data.Set("address", "berlin+germany")
			data.Set("active", "true")
			data.Set("live", "false")
			data.Set("money", "2550.50")
			data.Set("code", "10")
			data.Set("job", "qa")

			return data
		}, xhttp.StatusNoMatch},

		{"no match (missing +)", func() url.Values {
			data := url.Values{}
			data.Set("name", "nice name")
			data.Set("address", "berlin germany")
			data.Set("active", "true")
			data.Set("live", "false")
			data.Set("money", "2550.50")
			data.Set("code", "10")
			data.Set("job", "dev")

			return data
		}, xhttp.StatusNoMatch},

		{"no match (missing field)", func() url.Values {
			data := url.Values{}
			data.Set("name", "nice name")
			data.Set("address", "berlin+germany")
			data.Set("active", "true")
			data.Set("live", "false")
			data.Set("code", "10")
			data.Set("job", "dev")

			return data
		}, xhttp.StatusNoMatch},

		{"match (float)", func() url.Values {
			data := url.Values{}
			data.Set("name", "nice name")
			data.Set("address", "berlin+germany")
			data.Set("active", "true")
			data.Set("live", "false")
			data.Set("money", "2550.50")
			data.Set("code", "10")
			data.Set("job", "dev")

			return data
		}, http.StatusNoContent},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test", strings.NewReader(tc.data().Encode()))
			req.Header.Add(httpval.HeaderContentType, httpval.MIMEFormURLEncoded)
			res, err := httpClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			require.Equal(t, tc.expectedStatus, res.StatusCode)
		})
	}

	scoped.AssertNumberOfCalls(t, 2)
}
