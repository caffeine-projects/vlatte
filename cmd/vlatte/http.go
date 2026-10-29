package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/caffeine-projects/vlatte"
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

const (
	// _adminDir is where the command keeps the mocks its admin API adds, across restarts, in the server's work
	// directory. The default mock file patterns don't match it, so only the admin API's store loads its files.
	_adminDir = mockhttp.WorkDirVar + "/_admin"

	// _journalDir is where the command keeps the journal of the requests it serves, across restarts, in the
	// server's work directory.
	_journalDir = mockhttp.WorkDirVar + "/_journal"
)

// newHTTP creates the HTTP server of args, with the command's settings s, whose mock files name wh as an on_complete
// listener, whose templates read kv, and which logs to logOutput. The command serves the admin API, which keeps the
// mocks it adds across restarts, and keeps a journal of the requests it serves, unless settings turn them off: they
// apply after code. It returns the admin API too, nil when it is off.
func newHTTP(args []string, s settings, wh *webhook.WebHook, kv vlatte.KV, logOutput io.Writer) (*mockhttp.Server, *admin.Admin, error) {
	cb, api := s.setupHTTP(mockhttp.Setup())

	m, err := mockhttp.New(cb.
		KV(kv).
		Journal(_journalDir).
		LogOutput(logOutput).
		ConfigSources(mockhttp.DefaultConfigSources(args)...).
		OnCompleteListener("webhook", wh))

	return m, api, err
}

// startHTTP starts the HTTP server m, over TLS when its settings say. In the vlatte image, it listens on the image's
// port.
func startHTTP(m *mockhttp.Server) error {
	// The docker address wins over every source, and its port depends on the final https setting.
	if _, ok := os.LookupEnv(_dockerHostEnv); ok {
		_ = (&dockerConfigurer{}).Apply(m.Config())
	}

	if m.Config().UseHTTPS {
		return m.StartTLS()
	}

	return m.Start()
}

// stopRecording stops the recording of m, if one runs, and returns the exit code. A recording writes what it took: a
// call or a mock it couldn't record fails the run. Serving the recordings can fail too, such as for a mock ID a mock
// file holds: the server is exiting, so that is only reported.
func stopRecording(m *mockhttp.Server, stderr io.Writer) int {
	res, err := m.StopRecording(context.Background())
	if err != nil && !errors.Is(err, vlatte.ErrNotRecording) {
		_, _ = fmt.Fprintln(stderr, err)

		if res == nil || res.Status.Failed > 0 {
			return 1
		}
	}

	return 0
}
