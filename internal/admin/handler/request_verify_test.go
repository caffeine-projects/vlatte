package handler_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/caffeine-projects/vlatte/internal/admin/handler"
	"github.com/caffeine-projects/vlatte/internal/adminapi"
	"github.com/caffeine-projects/vlatte/journal"
)

// operationsOf returns the admin operations of a server whose journal is j.
func operationsOf(t *testing.T, j *journal.Journal) *handler.Operations {
	t.Helper()

	return handler.NewOperations(adminapi.Capabilities{Mocks: adminapi.MocksOf[*fakeMock](newStore(t), nil), Journal: j})
}

func TestOperations_VerifyRequestsWaitsForTheCountToPass(t *testing.T) {
	// A client checks a request the code under test sends in the background: the check passes as it comes.
	synctest.Test(t, func(t *testing.T) {
		j := journalOf(t)
		ops := operationsOf(t, j)

		go func() {
			time.Sleep(time.Second)
			require.NoError(t, j.Append(entryOf("a", "GET", "/a", journal.OutcomeMatched)))
		}()

		verdict, err := ops.VerifyRequests(t.Context(), []byte(`{"path": "/a", "within": "5s"}`), "json")
		require.NoError(t, err)
		require.True(t, verdict.Passed)
		require.Equal(t, int64(1000), verdict.WaitedMS)
	})
}

func TestOperations_VerifyRequestsHoldsDuring(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		j := journalOf(t)
		ops := operationsOf(t, j)

		verdict, err := ops.VerifyRequests(t.Context(), []byte(`{"count": 0, "during": "2s"}`), "json")
		require.NoError(t, err)
		require.True(t, verdict.Passed)
		require.Equal(t, int64(2000), verdict.WaitedMS)
	})
}

func TestOperations_VerifyRequestsEndsItsWait(t *testing.T) {
	// A wait holds a client's connection: the admin API's Close, a reset of the journal and the mock server's close
	// each end it, with why, rather than let it run out.
	tests := []struct {
		name   string
		end    func(ops *handler.Operations, j *journal.Journal) error
		status int
		code   string
	}{
		{
			name:   "the admin API closes",
			end:    func(ops *handler.Operations, _ *journal.Journal) error { ops.EndWaits(); return nil },
			status: 503,
			code:   "closing",
		},
		{
			name:   "the journal is cleared",
			end:    func(_ *handler.Operations, j *journal.Journal) error { return j.Clear() },
			status: 409,
			code:   "incomplete_journal",
		},
		{
			name:   "the mock server closes",
			end:    func(_ *handler.Operations, j *journal.Journal) error { return j.Close() },
			status: 503,
			code:   "closing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				j := journalOf(t, entryOf("a", "GET", "/a", journal.OutcomeMatched))
				ops := operationsOf(t, j)

				go func() {
					time.Sleep(time.Second)
					require.NoError(t, tt.end(ops, j))
				}()

				start := time.Now()
				_, err := ops.VerifyRequests(t.Context(), []byte(`{"count": 2, "within": "30s"}`), "json")
				require.Equal(t, time.Second, time.Since(start))

				doc := handler.Problem(err, "verify_requests")
				require.Equal(t, tt.status, doc.Status, doc.Detail)
				require.Equal(t, tt.code, doc.Code)
			})
		})
	}
}

func TestOperations_VerifyRequestsEndsTheWaitsThatStartOnceClosing(t *testing.T) {
	// A check that comes as the admin API closes waits for nothing.
	j := journalOf(t)
	ops := operationsOf(t, j)
	ops.EndWaits()

	_, err := ops.VerifyRequests(t.Context(), []byte(`{"within": "30s"}`), "json")
	require.Equal(t, "closing", handler.Problem(err, "verify_requests").Code)

	// A check that doesn't wait still runs.
	verdict, err := ops.VerifyRequests(t.Context(), []byte(`{"count": 0}`), "json")
	require.NoError(t, err)
	require.True(t, verdict.Passed)
}
