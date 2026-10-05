package e2e

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

type testBodyParser struct {
	content string
	action  func() error
}

func (p testBodyParser) CanParse(content string, _ *http.Request) bool {
	return content == p.content
}

func (p testBodyParser) Parse(_ []byte, _ *http.Request) (any, error) {
	return nil, p.action()
}

func TestLogging(t *testing.T) {
	m := mockhttp.MustNew(mockhttp.Setup().
		UseDescriptiveLogger().
		RequestBodyParsers(
			&testBodyParser{"PANIC", func() error { panic("BOOM") }},
			&testBodyParser{"ERROR", func() error { return errors.New("FAIL") }}))
	t.Cleanup(func() { m.Close() })
	m.MustStart()
	m.MustMock(mockhttp.Getf("/test").Reply(mockhttp.OK()))

	res, err := http.Get(m.URL("/test"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, err = http.Get(m.URL("/test/nowhere"))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)

	m.MustMock(mockhttp.Postf("/fail").Reply(mockhttp.OK()))

	req, _ := http.NewRequest(http.MethodPost, m.URL("/panic"), strings.NewReader("hi"))
	req.Header.Add(mockhttp.HeaderContentType, "PANIC")

	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
	require.Contains(t, string(b), "BOOM")

	m.MustMock(mockhttp.Postf("/fail").Reply(mockhttp.OK()))

	req, _ = http.NewRequest(http.MethodPost, m.URL("/fail"), strings.NewReader("hi"))
	req.Header.Add(mockhttp.HeaderContentType, "ERROR")

	res, err = http.DefaultClient.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	m.MustMock(mockhttp.Getf("/err").RequestMatches(func(_ *http.Request) (bool, error) {
		return false, errors.New("nope")
	}).Reply(mockhttp.OK()))

	res, err = http.Get(m.URL("/err"))

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, mockhttp.StatusNoMatch, res.StatusCode)
}
