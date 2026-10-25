// Package journal keeps the requests a mock server serves, of any protocol, whatever their outcome, so a test can
// verify after the fact what the code under test sent, as WireMock's verify does: a changelog of JSON lines in segment
// files, with an index in memory that narrows queries. Queries match with the matchers of mocks, such as those of
// package [github.com/caffeine-projects/vlatte/match]. A server embeds a [Verifier], next to its [vlatte.Base], to
// verify its requests.
//
// The servers of [github.com/caffeine-projects/vlatte/mockhttp] and [github.com/caffeine-projects/vlatte/mockgrpc] keep
// a journal when their setup asks for one. A [Journal] works on its own too, as a changelog of any requests: [New]
// opens one.
//
// # Entries
//
// An [Entry] is one request, with the fields every protocol has: its kind, its [Outcome], the mock that answered it,
// its method, path and full target, the status of its answer, its time, headers and body, and, for a request no mock
// matched, the mocks that came closest. [Journal.Append] gives each entry a sequence number, which orders them: a
// query returns the newest first, and [Journal.Get] finds one by its request id.
//
// # Files
//
// The journal never rewrites an entry. Each one is a line of JSON appended to the active segment, a file named
// journal-NNNNNN.log. A new segment starts once the active one holds a fourth of MaxEntries, 10 000 at most, or 64 MiB,
// so dropping the oldest entries deletes whole files. In a named directory, a segment that fills gets a sidecar,
// journal-NNNNNN.idx, with what the index keeps of its entries, as does the active one on Close: the next journal of
// the directory reads the sidecars instead of the logs, as long as each describes its log as the log is.
//
// A named directory keeps the entries across restarts, and gets a .gitignore that keeps its files out of git. An empty
// Dir keeps them in a temporary directory, which Close removes. One journal at a time opens a directory, where the
// system locks files: another fails with [ErrInUse].
//
// # The index
//
// The journal keeps a row of each entry in memory: its sequence number, request id and time, the fields a query
// selects by but headers and body, and where its line is on disk. For the indexed fields, kind, outcome, mock id,
// method, path and status, it also lists, for each value it holds, the entries that have it.
//
// # Queries
//
// A [Query] selects entries by those fields, by the full target, by time, and by headers and body; [Filter] values,
// such as the request patterns of a protocol, narrow them further. A query runs in steps, each reading less than the
// one before:
//
//  1. It copies the rows, and the values of the indexed fields it selects by, then lets go of the journal.
//  2. It runs each matcher of an indexed field once per value the journal holds, not once per entry, and takes the
//     entries of the values that match: the fields it selects by intersect.
//  3. It checks the rows left against the target and the time, which rows hold too.
//  4. Only then, and only when it selects by headers, body or filters, or returns entries, does it read their lines
//     from disk.
//
// So [Journal.Count] of a query of indexed fields alone reads no file. [Journal.Find] returns a [Page]: the entries,
// the count of all the query selects, and, for a query with a Limit, where the next page starts.
//
// # Writes and reads
//
// Append is synchronous: an entry is in the journal once Append returns. The mock servers append a request's entry as
// they end its answer, so a test checks it right after the request returns. Queries match and read the copy they took
// without holding the journal, so appends don't wait on them. A journal is safe for concurrent use.
//
// # Retention and recovery
//
// The journal keeps the newest MaxEntries entries, all of them for 0, and MaxBodySize bytes of each body: an entry
// whose body lost the rest says so. Dropping the oldest entries deletes the segments that hold none of the newest.
//
// A crash may cut a line short, or leave a file half written: the next journal of the directory leaves the line out,
// logs it, and removes the file. [Journal.Clear] writes a tombstone, journal.clear, before it deletes the segments, so
// a journal opened after a crash, or after a delete that failed, drops them all the same.
//
// # Verifying
//
// A [Verifier] verifies the requests of a server with [Verifier.Requests] and [Verifier.CountRequests], and asserts
// them in a test with [Verifier.AssertRequested] and [Verifier.AssertNotRequested], whose failures list the requests
// that matched, or, when none did, the newest ones. A protocol that implements [RequestPatterns] turns the request part
// of its own mocks into filters, so a test verifies a request as a mock matches one.
package journal
