package journal_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/journal"
)

func TestDraft_TheEntryTellsTheEventsThatFailed(t *testing.T) {
	// A request a mock answered, whose webhooks failed, looked fine in the journal. Its entry tells each event that
	// failed now, whichever goroutine it failed on, and a request that failed on its own keeps its own message.
	j := newJournal(t, journal.Options{})

	matched := j.Draft(*entry("matched"))

	var wg sync.WaitGroup
	for _, msg := range []string{"webhook at index 0: target down", "webhook at index 1: timeout"} {
		wg.Go(func() { matched.EventFailed(errors.New(msg)) })
	}

	wg.Wait()
	require.NoError(t, matched.Append(func(*journal.Entry) {}))

	got, err := j.Get("matched")
	require.NoError(t, err)
	require.ElementsMatch(t,
		[]string{"webhook at index 0: target down", "webhook at index 1: timeout"},
		strings.Split(got.Message, "\n"))

	failed := j.Draft(*entry("failed"))
	failed.EventFailed(errors.New("webhook at index 0: target down"))
	require.NoError(t, failed.Append(func(e *journal.Entry) {
		e.Outcome, e.Message = journal.OutcomeFailed, "http: reply failed"
	}))

	got, err = j.Get("failed")
	require.NoError(t, err)
	require.Equal(t, "http: reply failed", got.Message)

	quiet := j.Draft(*entry("quiet"))
	require.NoError(t, quiet.Append(func(*journal.Entry) {}))

	got, err = j.Get("quiet")
	require.NoError(t, err)
	require.Empty(t, got.Message, "no event failed")
}

func TestDraft_EachEventThatFailedIsALineOfTheMessage(t *testing.T) {
	// A callback that panics fails with its stack, over many lines. The message still has one line for each event that
	// failed, so a reader tells them apart.
	j := newJournal(t, journal.Options{})

	d := j.Draft(*entry("req-1"))
	stack := "goroutine 7 [running]:\n\tmain.go:12 +0x1d\r\n"
	d.EventFailed(fmt.Errorf("callback: panic: %v\n%s", "boom", stack))
	d.EventFailed(errors.New("webhook at index 1: timeout"))
	require.NoError(t, d.Append(func(*journal.Entry) {}))

	got, err := j.Get("req-1")
	require.NoError(t, err)
	require.Equal(t,
		[]string{"callback: panic: boom goroutine 7 [running]: main.go:12 +0x1d", "webhook at index 1: timeout"},
		strings.Split(got.Message, "\n"))
}

func TestDraft_TheFirstOutcomeCounts(t *testing.T) {
	// A request can end more than one way at once, such as a reply that fails as its client leaves: the journal keeps
	// one entry of it, as it was when it reached its first outcome.
	j := newJournal(t, journal.Options{})

	d := j.Draft(*entry("req-1"))
	require.Equal(t, "req-1", d.ID())

	d.Update(func(e *journal.Entry) { e.MockID = "m1" })
	require.NoError(t, d.Append(func(e *journal.Entry) { e.Outcome = journal.OutcomeAbandoned }))
	require.NoError(t, d.Append(func(e *journal.Entry) { e.Outcome = journal.OutcomeFailed }))
	d.Update(func(e *journal.Entry) { e.MockID = "m2" })
	d.EventFailed(errors.New("webhook at index 0: target down"))

	require.Equal(t, []string{"req-1"}, find(t, j, journal.Query{}))

	got, err := j.Get("req-1")
	require.NoError(t, err)
	require.Equal(t, journal.OutcomeAbandoned, got.Outcome)
	require.Equal(t, "m1", got.MockID)
	require.Empty(t, got.Message, "an event that fails once the entry is appended is left out")
}
