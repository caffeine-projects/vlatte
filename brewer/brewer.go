package brewer

import (
	"github.com/caffeine-projects/vlatte/brewer/xhttp"
)

func HTTP(options ...xhttp.Configurer) *xhttp.HTTPMockApp {
	return xhttp.New(options...)
}
