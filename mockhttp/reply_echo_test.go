package mockhttp

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplyEcho(t *testing.T) {
	m := MustNew()
	t.Cleanup(func() { m.Close() })
	m.MustStart()

	expectedBody := `{"status":"ok"}`
	scope := m.MustMock(Postf("/test").Reply(Echo().Log()))
	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodPost, m.URL("/test"), strings.NewReader(expectedBody))
	req.Header.Add(HeaderContentType, MIMETextPlainCharsetUTF8)

	res, err := httpClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	require.NoError(t, res.Body.Close())
	require.True(t, scope.AssertCalled(t))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, MIMETextPlainCharsetUTF8, res.Header.Get(HeaderContentType))
	require.Contains(t, string(b), expectedBody)
	require.Contains(t, string(b), res.Header.Get("x-custom-header"))
}

func TestEcho_DescribingItFailsSayingSo(t *testing.T) {
	// #42: mock files have no echo reply, so the description of an Echo mock failed to load back. Describing it fails
	// now, and says why, rather than describe a mock no file can hold.
	m := MustNew(Setup().Silent())

	_, err := EncodeMock(m.MustMock(Getf("/e").Reply(Echo())).Mocks()[0], "json")
	require.ErrorIs(t, err, errEchoNotDescribed)
}
