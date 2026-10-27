package main

import (
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte/mockhttp"
)

const (
	// _dockerHTTPPort and _dockerHTTPSPort are the ports of the mock server in the vlatte image, which no setting
	// changes.
	_dockerHTTPPort  = "8080"
	_dockerHTTPSPort = "8443"

	// _dockerAdminPort is the port of the admin API in the vlatte image.
	_dockerAdminPort = "8081"
)

type dockerConfigurer struct{}

func (c *dockerConfigurer) Apply(conf *mockhttp.Config) error {
	host := dockerHost()

	if conf.UseHTTPS {
		conf.Addr = host + _dockerHTTPSPort
	} else {
		conf.Addr = host + _dockerHTTPPort
	}

	return nil
}

// dockerHost returns the host the vlatte image listens on, followed by a colon: VLATTE_DOCKER_HOST, or every interface.
func dockerHost() string {
	host := strings.TrimSpace(os.Getenv(_dockerHostEnv))
	if host == "" {
		host = "0.0.0.0:"
	}

	if !strings.HasSuffix(host, ":") {
		host += ":"
	}

	return host
}

// adminAddr returns the address of the vlatte command's admin API: a random local port, or port 8081 of the docker
// host in the vlatte image, where a random port couldn't be published.
func adminAddr() string {
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		return dockerHost() + _dockerAdminPort
	}

	return "127.0.0.1:0"
}
