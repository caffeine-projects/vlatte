package journal_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/journal"
)

func TestEventErrors_TheEntryTellsTheEventsThatFailed(t *testing.T) {
	// A request a mock answered, whose webhooks failed, looked fine in the journal. Its entry tells each event that
	// failed now, whichever goroutine it failed on, and a request that failed on its own keeps its own message.
	var events journal.EventErrors

	var wg sync.WaitGroup
	for _, msg := range []string{"webhook at index 0: target down", "webhook at index 1: timeout"} {
		wg.Go(func() { events.Add(errors.New(msg)) })
	}

	wg.Wait()

	matched := journal.Entry{Outcome: journal.OutcomeMatched}
	events.Tell(&matched)
	require.ElementsMatch(t,
		[]string{"webhook at index 0: target down", "webhook at index 1: timeout"},
		strings.Split(matched.Message, "\n"))

	failed := journal.Entry{Outcome: journal.OutcomeFailed, Message: "http: reply failed"}
	events.Tell(&failed)
	require.Equal(t, "http: reply failed", failed.Message)

	var none journal.EventErrors

	quiet := journal.Entry{Outcome: journal.OutcomeMatched}
	none.Tell(&quiet)
	require.Empty(t, quiet.Message, "no event failed")
}
