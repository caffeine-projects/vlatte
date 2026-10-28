package verify

import (
	"context"
	"errors"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/journal"
)

var (
	// ErrNoRequestPatterns is the error of a selection by Matching on a server whose protocol takes no patterns.
	ErrNoRequestPatterns = errors.New("verify: the server takes no request patterns")

	// ErrInvalidSpec is the error of a check whose [Spec] can't run, such as one without a count.
	ErrInvalidSpec = errors.New("verify: invalid check")

	// ErrNoMethod is the error of a selection by Method on a server whose requests have no method, such as gRPC's.
	ErrNoMethod = errors.New("verify: the server's requests have no method: select by Path")

	// ErrOtherJournal is the error of a check from a mark of another server's journal.
	ErrOtherJournal = errors.New("verify: the mark is of another server's journal")

	// ErrCleared is the error of a check of a journal cleared after its mark, as it read, or during its wait: the
	// requests it counts are gone. A mark past the journal's newest request, of a journal that started over, is one.
	ErrCleared = errors.New("verify: the journal was cleared: the requests the check counts are gone")

	// ErrDropped is the error of a check whose window holds requests the journal dropped to keep its MaxEntries: a
	// count of those left could pass when it shouldn't.
	ErrDropped = errors.New("verify: the journal dropped requests the check counts: raise its MaxEntries")
)

// Pattern selects the entries of a journal that a protocol's request pattern matches, such as the request part of a
// mock builder, and names the parts of it an entry fails.
type Pattern interface {
	// Match tells whether the pattern matches e. A pattern that reads a part of e the journal cut fails with
	// [vlatte.ErrMatching] once the rest of it matches.
	Match(ctx context.Context, e *journal.Entry) (bool, error)

	// Explain tells which parts of the pattern e passes and which it fails, for a report. A part the journal cut is
	// neither.
	Explain(ctx context.Context, e *journal.Entry) (Miss, error)
}

// Miss is how many of the parts a check selects by an entry passed, of how many it was checked by, and the names of
// those it failed, such as "header Authorization" or "body".
type Miss struct {
	Passed int
	Total  int
	Failed []string
}

// Patterns is what the server of a protocol implements to turn b, a builder of its mocks, into a [Pattern], for
// [Requests.Matching] to select by the request part of its mocks.
type Patterns[TM vlatte.Mock, A any] interface {
	RequestPattern(b vlatte.Builder[TM, A]) (Pattern, error)
}

// Protocol tells a [Verifier] about the requests of a server's protocol.
type Protocol[TM vlatte.Mock, A any] struct {
	// Patterns turns builders of the server's mocks into patterns. Nil takes none: [Requests.Matching] fails with
	// [ErrNoRequestPatterns].
	Patterns Patterns[TM, A]

	// NoMethod tells that the protocol's requests have no method, as gRPC calls: [Requests.Method] fails with
	// [ErrNoMethod] rather than select none.
	NoMethod bool
}

// Verifier verifies the requests the journal of a mock server keeps. A server embeds it, next to its [vlatte.Base], so
// its methods are the server's own and every protocol verifies the same way. TM is the server's mock type and A the
// server, as in [vlatte.Base].
type Verifier[TM vlatte.Mock, A any] struct {
	j        *journal.Journal
	protocol Protocol[TM, A]
}

// NewVerifier returns the verifier of the requests j keeps, nil for a server that keeps none, of a server whose
// protocol p describes.
func NewVerifier[TM vlatte.Mock, A any](j *journal.Journal, p Protocol[TM, A]) *Verifier[TM, A] {
	return &Verifier[TM, A]{j: j, protocol: p}
}

// Journal returns the journal of the requests the server serves, nil when it keeps none.
func (v *Verifier[TM, A]) Journal() *journal.Journal {
	return v.j
}

// VerifyRequests returns the selection of every request the server's journal holds, whatever its outcome, which its
// methods narrow, then verify or read. A server keeps no journal unless its setup asks for one: every check of one
// that keeps none fails with [journal.ErrNoJournal].
//
// The journal holds the requests the server's mocks get: it doesn't hold those a middleware answers before a mock,
// such as CORS preflights. A request is in it once the server ends its answer, so a check right after a client gets a
// small answer finds it; one that a client reads as it comes, such as a stream, or that the code under test sends in
// the background, needs [Requests.Within].
func (v *Verifier[TM, A]) VerifyRequests() Requests[TM, A] {
	return Requests[TM, A]{v: v}
}

// JournalMark returns a mark of the server's journal as it is now, for [Requests.Since] to select the requests that
// come after it, and none of those before, such as other tests'.
func (v *Verifier[TM, A]) JournalMark() Mark {
	return Mark{j: v.j, seq: v.j.Last()}
}

// Mark is a point in a server's journal, which [Verifier.JournalMark] takes.
type Mark struct {
	j   *journal.Journal
	seq uint64
}
