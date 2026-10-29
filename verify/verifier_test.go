package verify_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/internal/testmock"
	"github.com/caffeine-projects/vlatte/journal"
	"github.com/caffeine-projects/vlatte/match"
	"github.com/caffeine-projects/vlatte/verify"
)

var (
	_ verify.Patterns[*fakeMock, fakeServer] = fakeServer{}
	_ verify.Pattern                         = pathPattern("")
)

var _t0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// fakeMock is a mock of no protocol. No test here builds one: a pattern is a builder, whose mock counts for nothing.
type fakeMock struct{}

func (*fakeMock) ID() string            { return "" }
func (*fakeMock) Name() string          { return "" }
func (*fakeMock) Priority() int         { return 0 }
func (*fakeMock) Hits() int64           { return 0 }
func (*fakeMock) Called() bool          { return false }
func (*fakeMock) Enabled() bool         { return true }
func (*fakeMock) Enable()               {}
func (*fakeMock) Disable()              {}
func (*fakeMock) Events() vlatte.Events { return vlatte.Events{} }
func (*fakeMock) String() string        { return "fake" }

// fakeServer is the server of a protocol whose mock builders are patterns of their own, such as pathPattern.
type fakeServer struct{}

func (fakeServer) RequestPattern(b vlatte.Builder[*fakeMock, fakeServer]) (verify.Pattern, error) {
	p, ok := b.(verify.Pattern)
	if !ok {
		return nil, errors.New("not a pattern")
	}

	return p, nil
}

// pathPattern is a pattern of fakeServer: the request part of a mock, here its path.
type pathPattern string

func (pathPattern) Build(fakeServer) (*fakeMock, error) {
	return nil, errors.New("a pattern builds no mock")
}

func (p pathPattern) Match(_ context.Context, e *journal.Entry) (bool, error) {
	return e.Path == string(p), nil
}

func (p pathPattern) Explain(_ context.Context, e *journal.Entry) (verify.Miss, error) {
	if e.Path == string(p) {
		return verify.Miss{Passed: 1, Total: 1}, nil
	}

	return verify.Miss{Total: 1, Failed: []string{"pattern path"}}, nil
}

// otherBuilder is a builder fakeServer takes no pattern of.
type otherBuilder struct{}

func (otherBuilder) Build(fakeServer) (*fakeMock, error) { return &fakeMock{}, nil }

func newJournal(t *testing.T, o journal.Options, entries ...*journal.Entry) *journal.Journal {
	t.Helper()

	if o.Dir == "" {
		o.Dir = t.TempDir()
	}

	j, err := journal.New(o)
	require.NoError(t, err)
	t.Cleanup(func() { j.Close() })

	for _, e := range entries {
		require.NoError(t, j.Append(e))
	}

	return j
}

// verifierOf returns the verifier of a server of fakeServer's protocol whose journal holds entries.
func verifierOf(t *testing.T, entries ...*journal.Entry) *verify.Verifier[*fakeMock, fakeServer] {
	t.Helper()

	j := newJournal(t, journal.Options{}, entries...)

	return verify.NewVerifier(j, verify.Protocol[*fakeMock, fakeServer]{Patterns: fakeServer{}})
}

func entry(id string, mutate ...func(e *journal.Entry)) *journal.Entry {
	e := &journal.Entry{
		ID:      id,
		Time:    _t0,
		Kind:    "http",
		Outcome: journal.OutcomeMatched,
		Method:  "GET",
		Path:    "/" + id,
		Target:  "http://127.0.0.1/" + id,
		Status:  200,
	}

	for _, m := range mutate {
		m(e)
	}

	return e
}

// ids returns a func that returns the ids of the entries a read found, such as ids(t)(r.Find(ctx)), once it found
// them without an error.
func ids(t *testing.T) func(entries []*journal.Entry, err error) []string {
	return func(entries []*journal.Entry, err error) []string {
		t.Helper()
		require.NoError(t, err)

		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e.ID)
		}

		return out
	}
}

// errorText returns the message of the one Errorf call ft received.
func errorText(t *testing.T, ft *testmock.FakeT) string {
	t.Helper()

	errs := ft.Errors()
	require.Len(t, errs, 1)

	return errs[0]
}

func TestVerifier_AServerWithoutAJournalFailsEveryCheck(t *testing.T) {
	// A server keeps no journal unless asked: a check of one fails with why, never passes for want of requests.
	v := verify.NewVerifier(nil, verify.Protocol[*fakeMock, fakeServer]{Patterns: fakeServer{}})
	require.Nil(t, v.Journal())

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Never(ft))
	require.Contains(t, errorText(t, ft), journal.ErrNoJournal.Error())

	_, err := v.VerifyRequests().Count(t.Context())
	require.ErrorIs(t, err, journal.ErrNoJournal)

	_, err = v.VerifyRequests().Find(t.Context())
	require.ErrorIs(t, err, journal.ErrNoJournal)
}

func TestVerifier_AMarkSelectsTheRequestsThatCameAfterIt(t *testing.T) {
	// A test that shares a server with the tests before it counts its own requests only.
	v := verifierOf(t, entry("a"))
	mark := v.JournalMark()
	require.NoError(t, v.Journal().Append(entry("b")))

	ft := testmock.NewFakeT()
	require.True(t, v.VerifyRequests().Since(mark).Times(ft, 1))
	require.True(t, v.VerifyRequests().Since(mark).Path(match.Equal("/a")).Never(ft))
	require.True(t, v.VerifyRequests().Times(ft, 2), "a selection without a mark counts all")
	ft.AssertNotCalled(t, "Errorf", mock.Anything, mock.Anything)

	require.Equal(t, []string{"b"}, ids(t)(v.VerifyRequests().Since(mark).Find(t.Context())))
}

func TestVerifier_AMarkOfAnotherServerFailsTheCheck(t *testing.T) {
	// Another server's journal numbers its requests apart: its mark says nothing of this one's.
	other := verifierOf(t, entry("a"), entry("b"))
	v := verifierOf(t, entry("a"))

	_, err := v.VerifyRequests().Since(other.JournalMark()).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrOtherJournal)
}

func TestVerifier_AMarkTheJournalWasClearedAfterFailsTheCheck(t *testing.T) {
	// The requests between the mark and the clear are gone: a count of those left could pass when it shouldn't.
	v := verifierOf(t, entry("a"))
	mark := v.JournalMark()
	require.NoError(t, v.Journal().Append(entry("b")))
	require.NoError(t, v.Journal().Clear())
	require.NoError(t, v.Journal().Append(entry("c")))

	_, err := v.VerifyRequests().Since(mark).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrCleared)

	ft := testmock.NewFakeT()
	require.False(t, v.VerifyRequests().Since(mark).AtLeast(ft, 1))
	require.Contains(t, errorText(t, ft), verify.ErrCleared.Error())

	// A mark taken after the clear counts what came since.
	require.True(t, v.VerifyRequests().Since(v.JournalMark()).Never(ft))
}

func TestVerifier_AProtocolWithoutPatternsRefusesMatching(t *testing.T) {
	v := verify.NewVerifier(newJournal(t, journal.Options{}), verify.Protocol[*fakeMock, fakeServer]{})

	_, err := v.VerifyRequests().Matching(pathPattern("/a")).Count(t.Context())
	require.ErrorIs(t, err, verify.ErrNoRequestPatterns)
}
