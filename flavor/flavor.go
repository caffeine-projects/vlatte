package flavor

import (
	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
)

func HTTP(options ...vlatte.Configurer[*xhttp.Config]) *xhttp.HTTPMockApp {
	return xhttp.MustNew(options...)
}
