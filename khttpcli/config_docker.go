package main

import (
	"os"
	"strings"

	"github.com/caffeine-projects/vlatte/khttp"
)

type dockerConfigurer struct {
}

func (c *dockerConfigurer) Apply(conf *khttp.Config) error {
	host := strings.TrimSpace(os.Getenv(_dockerHostEnv))
	if host == "" {
		host = "0.0.0.0:"
	}

	if !strings.HasSuffix(host, ":") {
		host += ":"
	}

	if conf.UseHTTPS {
		conf.Addr = host + "8443"
	} else {
		conf.Addr = host + "8080"
	}

	return nil
}
