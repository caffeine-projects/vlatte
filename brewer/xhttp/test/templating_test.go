package test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/caffeine-projects/vlatte/brewer/xhttp"
	"github.com/caffeine-projects/vlatte/shelf/kv/kvmem"

	"github.com/stretchr/testify/require"
)

// TODO: rework templating implementation.

func TestTemplating(t *testing.T) {
	t.Log("templating needs rework")
	t.SkipNow()

	kv := kvmem.New()
	m := xhttp.New(xhttp.Setup().Parameters(kv))
	m.MustStart()
	defer m.Close()

	err := kv.Set(t.Context(), "test", []byte("hi"))
	require.NoError(t, err)

	m.MustBrew(xhttp.FromFile("testdata/templating/templating_01.yaml"))

	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test/templating", strings.NewReader("hello world -> hi"))
	req.Header.Add("template", "true")
	req.AddCookie(&http.Cookie{Name: "cookie_test", Value: "cookie_value"})

	res, err := httpClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "test --> go --> POST --> true cookie_value Path 0 test Path 1 templating", string(b))
}

func TestTemplating_BodyFilename(t *testing.T) {
	t.Log("templating needs rework")
	t.SkipNow()

	m := xhttp.New()
	m.MustStart()
	defer m.Close()

	m.MustBrew(xhttp.FromFile("testdata/templating/templating_02.yaml"))

	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test/templating_02_data.txt", nil)
	res, err := httpClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "hello world\n", string(b))
}

func TestTemplating_BodyFilename_BodyTemplate(t *testing.T) {
	t.Log("templating needs rework")
	t.SkipNow()

	kv := kvmem.New()
	m := xhttp.New(xhttp.Setup().Parameters(kv))
	m.MustStart()
	defer m.Close()

	m.MustBrew(xhttp.FromFile("testdata/templating/templating_03.yaml"))

	err := kv.Set(t.Context(), "message", []byte("hello world"))
	require.NoError(t, err)

	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/templating_03_data.txt", nil)
	res, err := httpClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "Method is: GET\nMessage is: hello world\n", string(b))
}

func TestTemplating_Header(t *testing.T) {
	t.Log("templating needs rework")
	t.SkipNow()

	kv := kvmem.New()
	m := xhttp.New(xhttp.Setup().Parameters(kv))
	m.MustStart()
	defer m.Close()

	m.MustBrew(xhttp.FromFile("testdata/templating/templating_04.yaml"))

	err := kv.Set(t.Context(), "test", []byte("ok"))
	require.NoError(t, err)
	err = kv.Set(t.Context(), "context", []byte("test"))
	require.NoError(t, err)

	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodGet, m.URL()+"/test/templating", nil)
	res, err := httpClient.Do(req)

	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "ok", res.Header.Get("test"))
	require.Equal(t, "test", res.Header.Get("ctx"))
	require.Equal(t, "hi", res.Header.Get("message"))
}

func TestTemplating_All(t *testing.T) {
	t.Log("templating needs rework")
	t.SkipNow()

	kv := kvmem.New()
	m := xhttp.New(xhttp.Setup().Parameters(kv))
	m.MustStart()
	defer m.Close()

	err := kv.Set(t.Context(), "test", []byte("ok"))
	require.NoError(t, err)
	err = kv.Set(t.Context(), "context", []byte("test"))
	require.NoError(t, err)

	m.MustBrew(xhttp.FromFile("testdata/templating/templating_05.yaml"))

	httpClient := &http.Client{}

	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/test/templating", strings.NewReader("hello world -> ok"))
	req.Header.Add("template", "true")
	req.AddCookie(&http.Cookie{Name: "cookie_test", Value: "cookie_value"})

	res, err := httpClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "test --> go --> POST --> true cookie_value Path 0 test Path 1 templating", string(b))
	require.Equal(t, "ok", res.Header.Get("test"))
	require.Equal(t, "test", res.Header.Get("ctx"))
	require.Equal(t, "hi", res.Header.Get("message"))
}
