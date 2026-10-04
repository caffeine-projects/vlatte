package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The docker configurer would replace the address.
	t.Setenv(_dockerHostEnv, "")
	require.NoError(t, os.Unsetenv(_dockerHostEnv))

	port, err := getRandomPort()
	require.NoError(t, err)

	addr := "127.0.0.1:" + strconv.Itoa(port)
	go run(ctx, xhttp.Setup().Addr(addr))

	// A running server answers requests no mock matches with StatusNoMatch.
	// Connection errors mean it is not up yet: they are retried, never counted as success.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := http.Get("http://" + addr)
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, xhttp.StatusNoMatch, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)
}

type httpsConfigurer struct{ addr string }

func (c httpsConfigurer) Apply(conf *xhttp.Config) error {
	conf.Addr = c.addr
	conf.UseHTTPS = true
	return nil
}

func TestCLI_HTTPS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	t.Setenv(_dockerHostEnv, "")
	require.NoError(t, os.Unsetenv(_dockerHostEnv))

	port, err := getRandomPort()
	require.NoError(t, err)

	addr := "127.0.0.1:" + strconv.Itoa(port)
	go run(ctx, httpsConfigurer{addr: addr})

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	t.Cleanup(client.CloseIdleConnections)

	// With HTTPS enabled, the CLI must serve over TLS.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		res, err := client.Get("https://" + addr)
		if !assert.NoError(c, err) {
			return
		}

		_ = res.Body.Close()
		assert.Equal(c, xhttp.StatusNoMatch, res.StatusCode)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestDockerCLI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })
	t.Setenv(_dockerHostEnv, "localhost")

	sc := http.StatusTeapot

	go run(ctx)

	request := func() {
		max := 5
		attempts := 0
		success := false

		for range max {
			res, err := http.Get("http://localhost:8080")

			if err == nil && res.StatusCode == sc {
				res.Body.Close()
				success = true
				break
			}

			if attempts == max {
				t.FailNow()
			}

			<-time.After(2 * time.Second)

			attempts++
		}

		if !success {
			t.FailNow()
		}
	}

	request()

	cancel()
}

func getRandomPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	return port, nil
}
