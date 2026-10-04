package vlatte_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/flavor/xhttp"
)

type mockWithEvents struct {
	*xhttp.HTTPMock
	events vlatte.Events
}

func (m mockWithEvents) Events() vlatte.Events { return m.events }

func TestEventRunner_OnCompleteErrorsOnUnknownListener(t *testing.T) {
	runner := vlatte.NewEventRunner(nil, nil)
	mock := mockWithEvents{xhttp.NewMock(), vlatte.Events{OnComplete: vlatte.EventConfigs{{Name: "missing"}}}}

	// A listener name that was never registered is a configuration error, not a silent no-op.
	err := runner.OnComplete(context.Background(), mock, func(vlatte.EventConfig) vlatte.OnComplete { return nil })

	require.ErrorContains(t, err, "missing")
}
