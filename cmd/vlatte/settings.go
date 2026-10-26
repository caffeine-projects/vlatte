package main

import (
	"github.com/caffeine-projects/vlatte/admin"
	"github.com/caffeine-projects/vlatte/internal/config"
	"github.com/caffeine-projects/vlatte/mockhttp"
	"github.com/caffeine-projects/vlatte/webhook"
)

// settings are the command's own settings, which it reads from the sources the mock server reads: the server
// accepts and ignores them, as a library server must not serve an admin API settings ask for.
type settings struct {
	admin     bool
	adminAddr string
	adminDir  string
}

// defaultSettings serve the admin API, which keeps the mocks it adds across restarts: settings apply after them.
func defaultSettings() settings {
	return settings{admin: true, adminAddr: adminAddr(), adminDir: _adminDir}
}

func settingsSchema() *config.Schema[settings] {
	return config.NewSchema(
		// The file source reads the path the server's config setting names: the server documents it.
		config.Field[settings]{Key: "config", Short: "c", Kind: config.String, Hidden: true},
		config.Field[settings]{
			Key: "admin", Kind: config.Bool, Usage: "Serve the admin API, on an HTTP server of its own.", Default: "true",
			Set: func(s *settings, v config.Value) error { s.admin = v.Bool; return nil },
		},
		config.Field[settings]{
			Key: "admin.addr", Kind: config.String,
			Usage: "Address the admin API listens on: host:port, :port or port. Empty picks a random local port.",
			Set: func(s *settings, v config.Value) error {
				s.adminAddr = v.Str
				_, err := admin.New(admin.Addr(v.Str))

				return err
			},
		},
		config.Field[settings]{
			Key: "admin.dir", Kind: config.String,
			Usage: "Directory of the files that keep the mocks the admin API adds across restarts, which only the server " +
				"loads: keep it out of mock_files. Relative paths start at root_dir, and {workdir} at workdir. Empty keeps them " +
				"in memory.",
			Set: func(s *settings, v config.Value) error { s.adminDir = v.Str; return nil },
		},
	)
}

// loadSettings reads the command's settings from the sources of args, as the server reads its own.
func loadSettings(args []string) (settings, error) {
	s := defaultSettings()
	_, err := settingsSchema().LoadOnly(&s, mockhttp.DefaultConfigSources(args))

	return s, err
}

// newServer creates the server of args, whose mock files name wh as an on_complete listener. The command serves the
// admin API, which keeps the mocks it adds across restarts, and keeps a journal of the requests it serves, unless
// settings turn them off: they apply after code.
func newServer(args []string, wh *webhook.WebHook) (*mockhttp.Server, error) {
	s, err := loadSettings(args)
	if err != nil {
		return nil, err
	}

	return mockhttp.New(s.setup(mockhttp.Setup()).
		Journal(_journalDir).
		ConfigSources(mockhttp.DefaultConfigSources(args)...).
		OnCompleteListener("webhook", wh))
}

// setup applies s to the server's setup.
func (s settings) setup(cb *mockhttp.ConfigBuilder) *mockhttp.ConfigBuilder {
	if !s.admin {
		return cb
	}

	cb.Admin(admin.MustNew(admin.Addr(s.adminAddr)))

	if s.adminDir != "" {
		cb.AdminStore(mockhttp.FileStore(s.adminDir))
	}

	return cb
}
