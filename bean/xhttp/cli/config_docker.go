package main

import (
	"os"
	"strings"

	khttp "github.com/caffeine-projects/vlatte/bean/xhttp"
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
