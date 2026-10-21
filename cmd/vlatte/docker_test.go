package main

import (
	"os"
	"testing"

	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDockerDefaultHTTPHost(t *testing.T) {
	t.Setenv(_dockerHostEnv, "")

	config := &mockhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "0.0.0.0:8080", config.Addr)
}

func TestConfigDockerDefaultHTTPsHost(t *testing.T) {
	t.Setenv(_dockerHostEnv, "")

	config := &mockhttp.Config{}
	config.UseHTTPS = true

	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.True(t, config.UseHTTPS)
	assert.Equal(t, "0.0.0.0:8443", config.Addr)
}

func TestConfigDockerHostDefined(t *testing.T) {
	t.Setenv(_dockerHostEnv, "example.org")

	config := &mockhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "example.org:8080", config.Addr)
}

func TestConfigDockerHostDefinedWithDots(t *testing.T) {
	t.Setenv(_dockerHostEnv, "example.org:")

	config := &mockhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "example.org:8080", config.Addr)
}

func TestAdminAddr(t *testing.T) {
	// The admin API listens on a random local port, but in the vlatte image, on a port of the docker host that
	// can be published.
	t.Setenv(_dockerHostEnv, "")
	assert.Equal(t, "0.0.0.0:8081", adminAddr())

	t.Setenv(_dockerHostEnv, "example.org")
	assert.Equal(t, "example.org:8081", adminAddr())

	t.Setenv(_dockerHostEnv, "")
	require.NoError(t, os.Unsetenv(_dockerHostEnv))
	assert.Equal(t, "127.0.0.1:0", adminAddr())
}
