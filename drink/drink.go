package drink

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/drink/xhttp"
)

func HTTP(options ...vlatte.Configurer[*xhttp.Config]) *xhttp.HTTPMockApp {
	return xhttp.New(options...)
}
