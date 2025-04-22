package drink

import (
	"github.com/caffeine-projects/vlatte/drink/xhttp"
)

func HTTP(options ...xhttp.Configurer) *xhttp.HTTPMockApp {
	return xhttp.New(options...)
}
