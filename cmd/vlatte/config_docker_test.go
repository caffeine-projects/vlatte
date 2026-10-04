package main

import (
	"testing"

	"github.com/caffeine-projects/vlatte/flavor/xhttp"
	"github.com/stretchr/testify/assert"
)

func TestConfigDockerDefaultHTTPHost(t *testing.T) {
	t.Setenv(_dockerHostEnv, "")

	config := &xhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "0.0.0.0:8080", config.Addr)
}

func TestConfigDockerDefaultHTTPsHost(t *testing.T) {
	t.Setenv(_dockerHostEnv, "")

	config := &xhttp.Config{}
	config.UseHTTPS = true

	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.True(t, config.UseHTTPS)
	assert.Equal(t, "0.0.0.0:8443", config.Addr)
}

func TestConfigDockerHostDefined(t *testing.T) {
	t.Setenv(_dockerHostEnv, "example.org")

	config := &xhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "example.org:8080", config.Addr)
}

func TestConfigDockerHostDefinedWithDots(t *testing.T) {
	t.Setenv(_dockerHostEnv, "example.org:")

	config := &xhttp.Config{}
	conf := &dockerConfigurer{}
	err := conf.Apply(config)

	assert.NoError(t, err)
	assert.False(t, config.UseHTTPS)
	assert.Equal(t, "example.org:8080", config.Addr)
}
