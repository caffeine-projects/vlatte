package vlatte_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
)

type failingConfigurer struct{}

func (failingConfigurer) Apply(*xhttp.Config) error { return errors.New("bad config") }

func TestNew_ReturnsFlavorErrors(t *testing.T) {
	// A flavor that fails to build is reported, not panicked.
	_, err := vlatte.New(t.Context(), xhttp.Flavor(failingConfigurer{}))
	require.ErrorContains(t, err, "bad config")
}
